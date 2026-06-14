package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// runView drives the real cobra `view` command against an endpoint with stdout
// and stderr wired to SEPARATE buffers, exactly mirroring a shell
// `gpufleet view ... 1>out 2>err` redirection. It returns (stdout, stderr, err)
// so a test can assert WHICH stream each piece of output landed on — the
// TASK-0042 stream-separation contract. cmd.SetOut/SetErr point cobra's two
// streams at our buffers; the RunE writes the rendered view to OutOrStdout(),
// so it MUST land in the stdout buffer.
func runView(t *testing.T, endpoint string) (stdout, stderr string, err error) {
	t.Helper()
	return runViewArgs(t, "view", "--endpoint", endpoint)
}

// runViewArgs is runView with arbitrary args, so a test can exercise flags such
// as --full-uuid while keeping the same stdout/stderr stream-separation harness.
func runViewArgs(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	root := NewRootCmd()
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

// TestViewTableGoesToStdout is the core TASK-0042 regression lock: the rendered
// table is the command's PRIMARY output and MUST land on STDOUT, never STDERR.
// Before the fix cmd.Print routed via cobra's OutOrStderr() fallback and the
// whole 730-byte table went to stderr, so a shell `1>out 2>err` captured an
// empty stdout. This asserts the DEVICES header and a GPU- device row are in the
// stdout stream, and that the stderr stream is empty on the happy path.
func TestViewTableGoesToStdout(t *testing.T) {
	srv := agentStub(t)
	defer srv.Close()

	stdout, stderr, err := runView(t, srv.URL)
	if err != nil {
		t.Fatalf("view returned an error on a healthy agent: %v", err)
	}

	// The table — DEVICES header + GPU- device rows — is on STDOUT.
	if !strings.Contains(stdout, "DEVICES") {
		t.Errorf("DEVICES header must be on STDOUT, got stdout:\n%s", stdout)
	}
	if !strings.Contains(stdout, "GPU-healthy") || !strings.Contains(stdout, "GPU-idle") {
		t.Errorf("GPU- device rows must be on STDOUT, got stdout:\n%s", stdout)
	}
	if !strings.Contains(stdout, "gpufleet single-node view") {
		t.Errorf("view header must be on STDOUT, got stdout:\n%s", stdout)
	}

	// STDERR must be empty on the happy path — it is reserved for diagnostics /
	// real errors only.
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("STDERR must be empty on a successful view, got:\n%s", stderr)
	}

	// The table must NOT have leaked onto STDERR (the exact pre-fix regression).
	if strings.Contains(stderr, "DEVICES") || strings.Contains(stderr, "GPU-") {
		t.Errorf("regression: the table leaked onto STDERR:\n%s", stderr)
	}
}

// TestViewStdoutSurvives2DevNull proves the user-visible symptom is fixed:
// discarding stderr (`2>/dev/null`) must NOT lose the table. We model
// `2>/dev/null` by simply ignoring the stderr buffer and asserting stdout alone
// still carries the full table.
func TestViewStdoutSurvives2DevNull(t *testing.T) {
	srv := agentStub(t)
	defer srv.Close()

	stdout, _, err := runView(t, srv.URL) // stderr deliberately discarded (= 2>/dev/null)
	if err != nil {
		t.Fatalf("view error: %v", err)
	}
	for _, want := range []string{"DEVICES", "GPU-healthy", "GPU-idle", "RCA VERDICT"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("2>/dev/null must not lose %q from STDOUT; stdout:\n%s", want, stdout)
		}
	}
}

// TestViewStaleBannerGoesToStdout locks that the STALE banner is NORMAL output:
// a held-stale window still renders its table + STALE marker on STDOUT (not
// stderr), because it is a normal view result, not an error.
func TestViewStaleBannerGoesToStdout(t *testing.T) {
	srv := staleAgentStub(t)
	defer srv.Close()

	stdout, stderr, err := runView(t, srv.URL)
	if err != nil {
		t.Fatalf("a stale (but reachable) agent must not error: %v", err)
	}
	if !strings.Contains(stdout, "STALE") {
		t.Errorf("STALE banner must be on STDOUT:\n%s", stdout)
	}
	if !strings.Contains(stdout, "DEVICES") || !strings.Contains(stdout, "GPU-idle") {
		t.Errorf("stale render must KEEP the table on STDOUT:\n%s", stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("a stale view is normal output; STDERR must stay empty:\n%s", stderr)
	}
}

// TestViewNoDataBannerGoesToStdout locks that the friendly NO-DATA banner
// (never-collected agent) is NORMAL output on STDOUT, not an error on stderr.
func TestViewNoDataBannerGoesToStdout(t *testing.T) {
	srv := neverCollectedAgentStub(t)
	defer srv.Close()

	stdout, stderr, err := runView(t, srv.URL)
	if err != nil {
		t.Fatalf("a never-collected (but reachable) agent must not error: %v", err)
	}
	if !strings.Contains(stdout, "NO DATA") {
		t.Errorf("NO-DATA banner must be on STDOUT:\n%s", stdout)
	}
	if !strings.Contains(stdout, "has not collected") {
		t.Errorf("the friendly NO-DATA message must be on STDOUT:\n%s", stdout)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("a NO-DATA view is normal output; STDERR must stay empty:\n%s", stderr)
	}
}

// TestViewTransportErrorGoesToStderr is the negative half of the contract: when
// the agent is genuinely UNREACHABLE (a transport error, not a view), nothing is
// rendered to STDOUT and the command returns a non-nil error so the entrypoint
// can report it on STDERR. This proves we did not blindly route everything to
// stdout — real errors still belong on stderr.
func TestViewTransportErrorGoesToStderr(t *testing.T) {
	// A server that immediately closes connections ⇒ transport failure, not a
	// decodable never-collected 200/503.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatalf("server does not support hijack")
		}
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()

	stdout, _, err := runView(t, srv.URL)
	if err == nil {
		t.Fatalf("an unreachable agent must surface a transport error from the view command")
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("no view should be written to STDOUT on a transport error, got:\n%s", stdout)
	}
}
