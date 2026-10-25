package sandbox

import (
	"archive/tar"
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

type archiveEntry struct {
	name     string
	typeflag byte
	body     string
	link     string
}

// craft builds an archive by hand so tests can produce what an honest encoder never would.
func craft(t *testing.T, entries ...archiveEntry) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	tw := tar.NewWriter(&buffer)
	for _, e := range entries {
		flag := e.typeflag
		if flag == 0 {
			flag = tar.TypeReg
		}
		header := &tar.Header{Name: e.name, Typeflag: flag, Mode: 0o644, Linkname: e.link}
		if flag == tar.TypeReg {
			header.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if flag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buffer
}

const goodResult = `{"version":1,"exitCode":0,"timedOut":false,"stdout":"hi","stderr":"","durationMs":12}`

func testLimits() Limits {
	return Limits{MaxOutputFiles: 3, MaxOutputBytes: 1024, MaxStdioBytes: 8}
}

func TestRequestRoundTrip(t *testing.T) {
	in := Request{
		RunID: "run_1", Code: "print(1)", TimeoutSeconds: 30,
		Inputs:  []File{{Path: "orders.csv", Data: []byte("a,b\n1,2\n")}, {Path: "nested/dir/x.json", Data: []byte("{}")}},
		Outputs: []File{{Path: "report.csv", Data: []byte("x\n")}},
	}
	var buffer bytes.Buffer
	if err := EncodeRequest(&buffer, in); err != nil {
		t.Fatal(err)
	}
	out, err := DecodeRequest(&buffer, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if out.Code != "print(1)" || out.TimeoutSeconds != 30 || len(out.Inputs) != 2 || len(out.Outputs) != 1 {
		t.Fatalf("decoded request = %+v", out)
	}
	if string(out.Inputs[0].Data) != "a,b\n1,2\n" || out.Inputs[1].Path != "nested/dir/x.json" || out.Outputs[0].Path != "report.csv" {
		t.Fatalf("decoded files = %+v", out)
	}
	if out.Limits.MaxOutputFiles != MaxOutputFiles || out.Limits.MaxStdioBytes != MaxStdioBytes {
		t.Fatalf("default limits were not applied: %+v", out.Limits)
	}
}

func TestNormalizeRejectsUnsafeRequests(t *testing.T) {
	base := func() Request { return Request{Code: "print(1)", Inputs: []File{{Path: "a.csv", Data: []byte("x")}}} }
	cases := map[string]func(*Request){
		"empty code":         func(r *Request) { r.Code = "  \n" },
		"huge code":          func(r *Request) { r.Code = strings.Repeat("x", MaxCodeBytes+1) },
		"timeout too long":   func(r *Request) { r.TimeoutSeconds = MaxTimeoutSeconds + 1 },
		"negative timeout":   func(r *Request) { r.TimeoutSeconds = -1 },
		"parent path":        func(r *Request) { r.Inputs[0].Path = "../a.csv" },
		"absolute path":      func(r *Request) { r.Inputs[0].Path = "/etc/passwd" },
		"backslash":          func(r *Request) { r.Inputs[0].Path = `a\b.csv` },
		"drive letter":       func(r *Request) { r.Inputs[0].Path = "c:x.csv" },
		"dot segment":        func(r *Request) { r.Inputs[0].Path = "a/./b.csv" },
		"trailing slash":     func(r *Request) { r.Inputs[0].Path = "dir/" },
		"empty path":         func(r *Request) { r.Inputs[0].Path = "" },
		"duplicate":          func(r *Request) { r.Outputs = []File{{Path: "a.csv", Data: nil}} },
		"too many files":     func(r *Request) { r.Inputs = make([]File, MaxFiles+1) },
		"stdio limit":        func(r *Request) { r.Limits.MaxStdioBytes = MaxStdioBytes + 1 },
		"output files limit": func(r *Request) { r.Limits.MaxOutputFiles = MaxOutputFiles + 1 },
		"output bytes limit": func(r *Request) { r.Limits.MaxOutputBytes = MaxOutputBytes + 1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			request := base()
			mutate(&request)
			if _, err := request.Normalize(); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
	normalized, err := base().Normalize()
	if err != nil || normalized.TimeoutSeconds != DefaultTimeoutSeconds {
		t.Fatalf("valid request = %+v, %v", normalized, err)
	}
}

func TestResultRoundTripAndStdioTruncation(t *testing.T) {
	var buffer bytes.Buffer
	err := EncodeResult(&buffer, Result{ExitCode: 3, Stdout: "héllo wörld", Stderr: "err", Duration: 1500 * time.Millisecond, Outputs: []File{{Path: "b.csv", Data: []byte("2")}, {Path: "a.csv", Data: []byte("1")}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := DecodeResult(&buffer, testLimits())
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 3 || result.Duration != 1500*time.Millisecond || result.Stderr != "err" {
		t.Fatalf("result = %+v", result)
	}
	// Eight bytes must not split "ö": "héllo w" is 8 bytes (é is two) so the cut lands before ö.
	if result.Stdout != "héllo w" || !result.StdoutTruncated || result.StderrTruncated {
		t.Fatalf("stdout = %q truncated=%v", result.Stdout, result.StdoutTruncated)
	}
	if len(result.Outputs) != 2 || result.Outputs[0].Path != "a.csv" || result.Outputs[1].Path != "b.csv" {
		t.Fatalf("outputs are not sorted: %+v", result.Outputs)
	}
}

func TestDecodeResultRejectsHostileArchives(t *testing.T) {
	cases := map[string]struct {
		entries []archiveEntry
		want    error
	}{
		"symlink":          {[]archiveEntry{{name: "result.json", body: goodResult}, {name: "output/link", typeflag: tar.TypeSymlink, link: "/etc/passwd"}}, ErrProtocol},
		"hardlink":         {[]archiveEntry{{name: "result.json", body: goodResult}, {name: "output/link", typeflag: tar.TypeLink, link: "result.json"}}, ErrProtocol},
		"traversal":        {[]archiveEntry{{name: "result.json", body: goodResult}, {name: "output/../escape", body: "x"}}, ErrProtocol},
		"absolute":         {[]archiveEntry{{name: "result.json", body: goodResult}, {name: "/output/x", body: "x"}}, ErrProtocol},
		"foreign entry":    {[]archiveEntry{{name: "result.json", body: goodResult}, {name: "input/x", body: "x"}}, ErrProtocol},
		"duplicate output": {[]archiveEntry{{name: "result.json", body: goodResult}, {name: "output/x", body: "1"}, {name: "output/x", body: "2"}}, ErrProtocol},
		"duplicate result": {[]archiveEntry{{name: "result.json", body: goodResult}, {name: "result.json", body: goodResult}}, ErrProtocol},
		"missing result":   {[]archiveEntry{{name: "output/x", body: "1"}}, ErrProtocol},
		"bad json":         {[]archiveEntry{{name: "result.json", body: "{"}}, ErrProtocol},
		"wrong version":    {[]archiveEntry{{name: "result.json", body: `{"version":2}`}}, ErrProtocol},
		"guest error":      {[]archiveEntry{{name: "result.json", body: `{"version":1,"error":"tar failed"}`}}, ErrProtocol},
		"too many files":   {[]archiveEntry{{name: "result.json", body: goodResult}, {name: "output/1", body: "1"}, {name: "output/2", body: "2"}, {name: "output/3", body: "3"}, {name: "output/4", body: "4"}}, ErrOutputLimit},
		"too many bytes":   {[]archiveEntry{{name: "result.json", body: goodResult}, {name: "output/1", body: strings.Repeat("x", 600)}, {name: "output/2", body: strings.Repeat("y", 600)}}, ErrOutputLimit},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeResult(craft(t, c.entries...), testLimits()); !errors.Is(err, c.want) {
				t.Fatalf("error = %v, want %v", err, c.want)
			}
		})
	}
	if _, err := DecodeResult(strings.NewReader("not a tar archive at all"), testLimits()); !errors.Is(err, ErrProtocol) {
		t.Fatalf("garbage archive = %v", err)
	}
	if _, err := DecodeResult(craft(t, archiveEntry{name: "result.json", body: goodResult}), Limits{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing limits = %v", err)
	}
}

func TestDecodeResultAcceptsDirectoryEntries(t *testing.T) {
	archive := craft(t, archiveEntry{name: "output/", typeflag: tar.TypeDir}, archiveEntry{name: "result.json", body: goodResult}, archiveEntry{name: "output/sub/x.txt", body: "ok"})
	result, err := DecodeResult(archive, testLimits())
	if err != nil || len(result.Outputs) != 1 || result.Outputs[0].Path != "sub/x.txt" {
		t.Fatalf("result = %+v, %v", result, err)
	}
}

func TestDecodeRequestRejectsForeignEntries(t *testing.T) {
	if _, err := DecodeRequest(craft(t, archiveEntry{name: "etc/passwd", body: "x"}), 1<<20); !errors.Is(err, ErrProtocol) {
		t.Fatalf("foreign entry = %v", err)
	}
	if _, err := DecodeRequest(craft(t, archiveEntry{name: "input/x", body: "x"}), 1<<20); !errors.Is(err, ErrProtocol) {
		t.Fatalf("missing metadata = %v", err)
	}
}

func TestTruncateUTF8(t *testing.T) {
	cases := []struct {
		in    string
		limit int
		want  string
		cut   bool
	}{
		{"abc", 3, "abc", false},
		{"abc", 2, "ab", true},
		{"éé", 3, "é", true},
		{"éé", 4, "éé", false},
		{"日本語", 4, "日", true},
		{"abc", 0, "", true},
	}
	for _, c := range cases {
		got, cut := truncateUTF8(c.in, c.limit)
		if got != c.want || cut != c.cut {
			t.Fatalf("truncateUTF8(%q,%d) = %q,%v; want %q,%v", c.in, c.limit, got, cut, c.want, c.cut)
		}
	}
	if got := truncateText("  line one\nline two\x00 ", 12); got != "line one lin" {
		t.Fatalf("truncateText = %q", got)
	}
}
