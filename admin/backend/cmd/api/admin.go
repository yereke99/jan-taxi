package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/mail"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/yereke99/jantaxi/admin/backend/internal/audit"
	"github.com/yereke99/jantaxi/admin/backend/internal/auth"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/db"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/password"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/textutil"
)

const (
	minPasswordLen = 12
	maxPasswordLen = 256
)

func cmdAdmin(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "create" {
		fmt.Fprintln(stderr, "usage: api admin create --email EMAIL --role ROLE [--name NAME]")
		return 2
	}
	fs := flag.NewFlagSet("admin create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	email := fs.String("email", "", "administrator email (login)")
	role := fs.String("role", "", "one of: "+rolesList())
	name := fs.String("name", "", "full name shown in the admin panel")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	in, err := validateAdminInput(*email, *role, *name)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	pw, err := readPassword(stdin, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := validatePassword(pw); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	url, err := databaseURL()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := db.Open(ctx, url, 2)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer pool.Close()

	id, err := createAdmin(ctx, pool, in, pw)
	switch {
	case db.IsUniqueViolation(err, ""):
		fmt.Fprintln(stderr, "an administrator with this email already exists")
		return 1
	case err != nil:
		fmt.Fprintln(stderr, "create administrator:", err)
		return 1
	}
	fmt.Fprintf(stdout, "created administrator %s (%s)\n", id, in.role)
	return 0
}

type adminInput struct {
	email, name string
	role        auth.Role
}

func validateAdminInput(email, role, name string) (adminInput, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
		return adminInput{}, errors.New("--email must be a plain email address")
	}
	r := auth.Role(strings.TrimSpace(role))
	if !r.Valid() {
		return adminInput{}, fmt.Errorf("--role must be one of: %s", rolesList())
	}
	n, err := textutil.CleanText(name, 120)
	if err != nil {
		return adminInput{}, errors.New("--name must be at most 120 characters")
	}
	return adminInput{email: email, name: n, role: r}, nil
}

func validatePassword(pw string) error {
	n := utf8.RuneCountInString(pw)
	if n < minPasswordLen || n > maxPasswordLen {
		return fmt.Errorf("password must be %d-%d characters", minPasswordLen, maxPasswordLen)
	}
	return nil
}

func rolesList() string {
	names := make([]string, len(auth.Roles))
	for i, r := range auth.Roles {
		names[i] = string(r)
	}
	return strings.Join(names, ", ")
}

func createAdmin(ctx context.Context, pool interface {
	Begin(context.Context) (pgx.Tx, error)
}, in adminInput, pw string) (uuid.UUID, error) {
	hash, err := password.Hash(pw)
	if err != nil {
		return uuid.Nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO admin_users (email, full_name, password_hash, role)
		VALUES ($1, $2, $3, $4) RETURNING id`, in.email, in.name, hash, string(in.role)).Scan(&id)
	if err != nil {
		return uuid.Nil, err
	}
	err = audit.Log(ctx, tx, nil, audit.Entry{
		Action:     "admin.create",
		EntityType: "admin",
		EntityID:   id.String(),
		After:      map[string]any{"email": in.email, "role": in.role, "full_name": in.name, "source": "cli"},
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, tx.Commit(ctx)
}

// readPassword reads the password from stdin. On a terminal it prompts twice
// with echo disabled; otherwise (a pipe or file) it reads the first line.
func readPassword(stdin io.Reader, stderr io.Writer) (string, error) {
	f, isFile := stdin.(*os.File)
	if isFile && isTerminal(f) {
		return promptPassword(f, stderr)
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read password: %w", err)
	}
	pw := strings.TrimRight(line, "\r\n")
	if pw == "" {
		return "", errors.New("no password on stdin (pipe it in, e.g. printf '%s\\n' \"$PASSWORD\" | api admin create ...)")
	}
	return pw, nil
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func promptPassword(tty *os.File, stderr io.Writer) (string, error) {
	restore := disableEcho(tty)
	defer restore()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() {
		if _, ok := <-sig; ok {
			restore()
			os.Exit(130)
		}
	}()

	r := bufio.NewReader(tty)
	read := func(prompt string) (string, error) {
		fmt.Fprint(stderr, prompt)
		line, err := r.ReadString('\n')
		fmt.Fprintln(stderr)
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	first, err := read("Password: ")
	if err != nil {
		return "", err
	}
	second, err := read("Repeat password: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("passwords do not match")
	}
	return first, nil
}

// disableEcho turns terminal echo off with stty (best effort; the standard
// library has no portable terminal API) and returns a function restoring it.
func disableEcho(tty *os.File) func() {
	off := exec.Command("stty", "-echo")
	off.Stdin = tty
	if off.Run() != nil {
		return func() {}
	}
	return func() {
		on := exec.Command("stty", "echo")
		on.Stdin = tty
		_ = on.Run()
	}
}
