package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/a6n-ai/inscribe/internal/config"
)

const defaultServeAddr = "127.0.0.1:7331"

func serveCmd(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := flags.String("addr", defaultServeAddr, "loopback address to listen on")
	if err := flags.Parse(args); err != nil {
		return err
	}
	host, port, err := loopbackPort(*addr)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if errors.Is(err, syscall.EADDRINUSE) {
		return addrInUse(*addr, port, stdout)
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "serving on http://%s\n", *addr)
	srv := &http.Server{Handler: newServeHandler(config.Home(), host, port), ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(ln)
}

// addrInUse explains a taken port: an Inscribe dashboard already serving there
// is reported as running (not an error); anything else names a free port to use.
func addrInUse(addr string, port int, stdout io.Writer) error {
	client := http.Client{Timeout: time.Second}
	if resp, err := client.Get("http://" + addr + "/"); err == nil {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK && bytes.Contains(body, []byte("<title>Inscribe")) {
			_, err := fmt.Fprintf(stdout, "inscribe serve is already running at http://%s\n", addr)
			return err
		}
	}
	host, _, _ := net.SplitHostPort(addr)
	next := net.JoinHostPort(host, strconv.Itoa(port+1))
	return fmt.Errorf("%s is in use by another program; run: inscribe serve --addr %s", addr, next)
}

// loopbackPort checks that addr is exactly 127.0.0.1 or ::1 with a port, and returns
// the host and port. Hostnames and other spellings are refused so the Host allowlist
// matches what the listener binds.
func loopbackPort(addr string) (string, int, error) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("--addr %q: %w", addr, err)
	}
	if host == "localhost" {
		return "", 0, errors.New("--addr: use 127.0.0.1 instead of localhost")
	}
	if host != "127.0.0.1" && host != "::1" {
		return "", 0, fmt.Errorf("--addr %q: host must be 127.0.0.1 or ::1", addr)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("--addr %q: invalid port", addr)
	}
	return host, port, nil
}

func newServeHandler(home, host string, port int) http.Handler {
	p := strconv.Itoa(port)
	hosts := map[string]bool{"127.0.0.1:" + p: true, "localhost:" + p: true}
	if host == "::1" {
		hosts["[::1]:"+p] = true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { serveOverview(w, r, home) })
	mux.HandleFunc("GET /recall", func(w http.ResponseWriter, r *http.Request) { serveRecall(w, r, home) })
	mux.HandleFunc("GET /memories", func(w http.ResponseWriter, r *http.Request) { serveMemories(w, r, home) })
	mux.HandleFunc("GET /memories/{id}", func(w http.ResponseWriter, r *http.Request) { serveMemory(w, r, home) })
	mux.HandleFunc("POST /memories/{id}/forget", func(w http.ResponseWriter, r *http.Request) { serveForget(w, r, home) })
	mux.HandleFunc("GET /discover", func(w http.ResponseWriter, r *http.Request) { serveDiscover(w, r, home) })
	mux.HandleFunc("POST /memories/{id}/rate", func(w http.ResponseWriter, r *http.Request) { serveRate(w, r, home) })
	mux.HandleFunc("POST /replay", func(w http.ResponseWriter, r *http.Request) { serveReplay(w, r, home) })
	mux.HandleFunc("POST /memories/forget", func(w http.ResponseWriter, r *http.Request) { serveForgetMany(w, r, home) })
	mux.HandleFunc("GET /how", func(w http.ResponseWriter, r *http.Request) { render(w, "how", nil) })
	mux.HandleFunc("GET /settings", func(w http.ResponseWriter, r *http.Request) { serveSettings(w, r, home) })
	mux.HandleFunc("POST /settings", func(w http.ResponseWriter, r *http.Request) { serveSaveSettings(w, r, home) })
	mux.HandleFunc("POST /share", func(w http.ResponseWriter, r *http.Request) { serveShare(w, r, home) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
		if !hosts[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func serveShare(w http.ResponseWriter, r *http.Request, home string) {
	if !sameOrigin(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "the form could not be read", http.StatusBadRequest)
		return
	}
	v := r.PostFormValue("analytics")
	if v != "0" && v != "1" {
		http.Error(w, `analytics must be "0" or "1"`, http.StatusBadRequest)
		return
	}
	if err := setShareAnalytics(home, v == "1"); err != nil {
		var perr toml.ParseError
		if errors.As(err, &perr) {
			http.Error(w, "config.toml does not parse; fix it by hand", http.StatusBadRequest)
			return
		}
		if errors.Is(err, errShareNotTable) {
			http.Error(w, "config.toml has a share key that is not a table; fix it by hand", http.StatusBadRequest)
			return
		}
		http.Error(w, "the config change failed; run inscribe doctor", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// sameOrigin refuses a change that a page on another origin could have sent.
func sameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
		http.Error(w, "cross-origin change refused", http.StatusForbidden)
		return false
	}
	if sf := r.Header.Get("Sec-Fetch-Site"); sf != "" && sf != "same-origin" && sf != "none" {
		http.Error(w, "cross-site change refused", http.StatusForbidden)
		return false
	}
	return true
}

var errShareNotTable = errors.New("share is not a table")

// setShareAnalytics sets share.analytics in the global config and leaves every other
// key as it was. Comments in an existing file are not kept.
func setShareAnalytics(home string, on bool) error {
	return setConfigValues(home, map[string]map[string]any{"share": {"analytics": on}})
}

// setConfigValues sets table.key values in the global config, "" naming the top
// level, and leaves every other key as it was. Comments in an existing file are not kept.
func setConfigValues(home string, set map[string]map[string]any) error {
	doc := map[string]any{}
	raw, err := os.ReadFile(filepath.Join(home, "config.toml")) //nolint:gosec // G304: the config path is under the user's INSCRIBE_HOME
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	default:
		if _, err := toml.Decode(string(raw), &doc); err != nil {
			return err
		}
	}
	for table, kv := range set {
		target := doc
		if table != "" {
			target = map[string]any{}
			if cur, ok := doc[table]; ok {
				if target, ok = cur.(map[string]any); !ok {
					if table == "share" {
						return errShareNotTable
					}
					return fmt.Errorf("%w: %s", errNotTable, table)
				}
			}
			doc[table] = target
		}
		maps.Copy(target, kv)
	}
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if err := enc.Encode(doc); err != nil {
		return err
	}
	return writeConfigFile(home, buf.Bytes())
}

var errNotTable = errors.New("config key is not a table")

// writeConfigFile replaces config.toml through a temp file in the same directory, so a
// crash never leaves a half-written config.
func writeConfigFile(home string, data []byte) error {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	path := filepath.Join(home, "config.toml")
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config.toml.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	// Some platforms cannot fsync a directory; the rename already happened.
	if d, err := os.Open(dir); err == nil { //nolint:gosec // G304: dir is the parent of the file this function just wrote
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
