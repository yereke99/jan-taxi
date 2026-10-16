package app

import (
	"bufio"
	"bytes"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/yereke99/jantaxi/admin/backend/api"
	"github.com/yereke99/jantaxi/admin/backend/internal/platform/testutil"
)

// TestRoutesDocumented fails when a mounted route is missing from
// api/openapi.yaml. It is opt-in (JT_CHECK_OPENAPI=1) because the contract and
// the handlers are written in parallel; CI enables it.
func TestRoutesDocumented(t *testing.T) {
	if os.Getenv("JT_CHECK_OPENAPI") != "1" {
		t.Skip("set JT_CHECK_OPENAPI=1 to check routes against api/openapi.yaml")
	}
	pool := testutil.NewDB(t)
	a := newTestApp(t, pool, testConfig(t))

	documented := specOperations(api.Spec)
	seen := map[string]bool{}
	var missing []string
	for _, h := range []chi.Routes{a.root, a.internalRoot} {
		err := chi.Walk(h, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			switch method {
			case http.MethodHead, http.MethodOptions, http.MethodConnect, http.MethodTrace:
				return nil
			}
			if strings.HasSuffix(route, "/*") {
				return nil // wildcard mounts are not individual operations
			}
			if op := method + " " + route; !seen[op] && !isDocumented(documented, method, route) {
				seen[op] = true
				missing = append(missing, op)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%d routes are not documented in api/openapi.yaml:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

var paramRe = regexp.MustCompile(`\{[^}]*\}`)

// normalizePath makes chi patterns and OpenAPI paths comparable: parameter
// names (and chi regexps) are erased and trailing slashes dropped.
func normalizePath(p string) string {
	p = paramRe.ReplaceAllString(p, "{}")
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return p
}

// isDocumented accepts the full route path or, for /api/v1 routes, the path
// relative to the spec's /api/v1 server URL.
func isDocumented(ops map[string]bool, method, route string) bool {
	m := strings.ToUpper(method)
	candidates := []string{route}
	if rest, ok := strings.CutPrefix(route, "/api/v1"); ok && rest != "" {
		candidates = append(candidates, rest)
	}
	for _, c := range candidates {
		n := normalizePath(c)
		if ops[m+" "+n] || ops["* "+n] {
			return true
		}
	}
	return false
}

var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true, "options": true, "head": true, "patch": true, "trace": true,
}

// specOperations extracts "METHOD /path" pairs from the block-style `paths:`
// mapping of an OpenAPI YAML document. A path item that is only a $ref is
// recorded as "* /path". This is a deliberately small reader: the spec is
// written in conventional block style.
func specOperations(spec []byte) map[string]bool {
	ops := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(spec))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	inPaths := false
	pathIndent, opIndent := -1, -1
	current := ""
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t\r")
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(trimmed)
		if indent == 0 {
			inPaths = strings.HasPrefix(trimmed, "paths:")
			pathIndent, opIndent, current = -1, -1, ""
			continue
		}
		if !inPaths {
			continue
		}
		key, _, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.Trim(key, `"'`)
		if pathIndent < 0 && strings.HasPrefix(key, "/") {
			pathIndent = indent
		}
		switch {
		case indent == pathIndent && strings.HasPrefix(key, "/"):
			current, opIndent = normalizePath(key), -1
		case current != "" && indent > pathIndent:
			if opIndent < 0 {
				opIndent = indent
			}
			if indent != opIndent {
				continue
			}
			if httpMethods[strings.ToLower(key)] {
				ops[strings.ToUpper(key)+" "+current] = true
			} else if key == "$ref" {
				ops["* "+current] = true
			}
		}
	}
	return ops
}

func TestSpecOperationsParser(t *testing.T) {
	spec := []byte(`openapi: 3.1.0
servers:
  - url: /api/v1
paths:
  /orders:
    get:
      summary: list
      parameters:
        - name: get
    post:
      summary: create
  "/orders/{id}":
    get: {}
  /internal/v1/bots/{bot}/lease:
    post:
      responses: {}
  /shared:
    $ref: '#/components/pathItems/Shared'
components:
  schemas:
    get:
      type: object
`)
	ops := specOperations(spec)
	want := []string{"GET /orders", "POST /orders", "GET /orders/{}", "POST /internal/v1/bots/{}/lease", "* /shared"}
	for _, w := range want {
		if !ops[w] {
			t.Errorf("missing %q in %v", w, ops)
		}
	}
	if len(ops) != len(want) {
		t.Errorf("unexpected operations: %v", ops)
	}
	if !isDocumented(ops, "get", "/api/v1/orders/{orderID:[0-9a-f-]+}") ||
		!isDocumented(ops, "POST", "/internal/v1/bots/{name}/lease") ||
		!isDocumented(ops, "DELETE", "/api/v1/shared/") ||
		isDocumented(ops, "DELETE", "/api/v1/orders/{id}") {
		t.Error("isDocumented matching")
	}
}
