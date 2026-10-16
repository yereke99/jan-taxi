// Command api is the Jan Taxi API server and its operational CLI:
//
//	api [serve] [--migrate=true]        run the public and internal HTTP servers
//	api migrate up|status               apply or inspect database migrations
//	api admin create --email E --role R [--name N]
//	                                    create an administrator; the password is read from stdin
//	api healthcheck [--url URL]         exit 0 when the server's /healthz answers 200
//	api version                         print build information
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return cmdServe(args, stderr)
	case "migrate":
		return cmdMigrate(args, stdout, stderr)
	case "admin":
		return cmdAdmin(args, stdin, stdout, stderr)
	case "healthcheck":
		return cmdHealthcheck(args, stderr)
	case "version":
		fmt.Fprintln(stdout, version())
		return 0
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", cmd)
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  api [serve] [--migrate=true]
  api migrate up|status
  api admin create --email EMAIL --role ROLE [--name NAME]   (password on stdin)
  api healthcheck [--url http://127.0.0.1:8080/healthz]
  api version
`)
}

// buildVersion is the release version, set at build time with
// -ldflags "-X main.buildVersion=<version>" (container images are built
// without VCS metadata, so the revision is unknown there).
var buildVersion string

func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		if buildVersion != "" {
			return "jantaxi-api " + buildVersion
		}
		return "jantaxi-api (unknown build)"
	}
	if buildVersion != "" {
		return fmt.Sprintf("jantaxi-api %s %s", buildVersion, info.GoVersion)
	}
	rev, modified := "unknown", ""
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				modified = " (modified)"
			}
		}
	}
	return fmt.Sprintf("jantaxi-api %s%s %s", rev, modified, info.GoVersion)
}
