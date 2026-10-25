package platform

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestSanitizeNameKeepsFilesSafeAndRecognizable(t *testing.T) {
	cases := map[string]string{
		"orders.csv":                      "orders.csv",
		"订单 汇总(10月).csv":                  "订单 汇总(10月).csv",
		`C:\Users\me\data.csv`:            "data.csv",
		"../../etc/passwd.txt":            "passwd.txt",
		"a/b/c.json":                      "c.json",
		"weird<>|name?.csv":               "weird___name_.csv",
		"  spaced.csv  ":                  "spaced.csv",
		strings.Repeat("x", 300) + ".csv": strings.Repeat("x", maxNameBytes-4) + ".csv",
	}
	for in, want := range cases {
		got, err := sanitizeName(in)
		if err != nil || got != want {
			t.Errorf("sanitizeName(%.30q) = %.40q, %v; want %.40q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", ".", "..", "...", "/", "  "} {
		if got, err := sanitizeName(bad); err == nil {
			t.Errorf("sanitizeName(%q) = %q, want a rejection", bad, got)
		}
	}
	// Leading dots are stripped, so a hidden-file trick cannot survive; a lone NUL becomes plain text.
	if got, err := sanitizeName(".hidden.csv"); err != nil || got != "hidden.csv" {
		t.Errorf("hidden file = %q, %v", got, err)
	}
	if got, err := sanitizeName("a\x00b.csv"); err != nil || got != "a_b.csv" {
		t.Errorf("NUL in name = %q, %v", got, err)
	}
	if got, _ := sanitizeName(strings.Repeat("日", 100) + ".csv"); len(got) > maxNameBytes || !strings.HasSuffix(got, ".csv") {
		t.Errorf("long name = %d bytes %q", len(got), got)
	}
}

func TestUploadsAreValidatedBeforeTheyBecomeReadable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	project := h.project(1_000_000)
	init := func(name string, size int64, sha string) (UploadTicket, error) {
		return h.svc.InitUpload(ctx, h.alice, InitUploadInput{ProjectID: project.ID, Name: name, Size: size, SHA256: sha}, key())
	}
	for name, attempt := range map[string]struct {
		name string
		size int64
		sha  string
	}{
		"executable":  {"tool.exe", 10, ""},
		"no size":     {"a.csv", 0, ""},
		"huge":        {"a.csv", 1 << 40, ""},
		"bad digest":  {"a.csv", 10, "XYZ"},
		"no name":     {"", 10, ""},
		"unsupported": {"photo.png", 10, ""},
	} {
		if _, err := init(attempt.name, attempt.size, attempt.sha); h.code(err) != "VALIDATION_ERROR" {
			t.Errorf("%s: %v", name, err)
		}
	}
	upload := func(name, content string, declared int64) (ArtifactView, error) {
		ticket, err := init(name, declared, "")
		h.must(err)
		if ticket.Status != ArtifactPending || ticket.UploadURL != "/files/"+ticket.ID+"/content" {
			t.Fatalf("ticket = %+v", ticket)
		}
		return h.svc.UploadContent(ctx, h.alice, ticket.ID, strings.NewReader(content))
	}
	if _, err := upload("short.csv", "a,b\n1,2\n", 100); h.code(err) != "SIZE_MISMATCH" {
		t.Fatalf("short body = %v", err)
	}
	if _, err := upload("long.csv", "a,b\n1,2\n", 3); h.code(err) != "FILE_TOO_LARGE" {
		t.Fatalf("long body = %v", err)
	}
	if _, err := upload("binary.csv", "a\n\xff\xfe\n", 5); h.code(err) != "INVALID_FILE" {
		t.Fatalf("invalid UTF-8 = %v", err)
	}
	if _, err := upload("fake.xlsx", "not a zip file", 14); h.code(err) != "INVALID_FILE" {
		t.Fatalf("fake workbook = %v", err)
	}
	checked, err := h.svc.InitUpload(ctx, h.alice, InitUploadInput{ProjectID: project.ID, Name: "sum.csv", Size: 4, SHA256: strings.Repeat("0", 64)}, key())
	h.must(err)
	if _, err = h.svc.UploadContent(ctx, h.alice, checked.ID, strings.NewReader("a\n1\n")); h.code(err) != "CHECKSUM_MISMATCH" {
		t.Fatalf("wrong checksum = %v", err)
	}
	if h.blobs.Len() != 0 {
		t.Fatalf("rejected uploads left %d bodies behind", h.blobs.Len())
	}

	// A good upload becomes ready, is idempotent, and records what was actually stored.
	idem := key()
	body := "order_id,amount\nA-1,10\n"
	ticket, err := h.svc.InitUpload(ctx, h.alice, InitUploadInput{ProjectID: project.ID, Name: "../orders.csv", Size: int64(len(body))}, idem)
	h.must(err)
	same, err := h.svc.InitUpload(ctx, h.alice, InitUploadInput{ProjectID: project.ID, Name: "orders.csv", Size: int64(len(body))}, idem)
	h.must(err)
	if same.ID != ticket.ID || ticket.Name != "orders.csv" {
		t.Fatalf("init is not idempotent or did not sanitize: %+v vs %+v", ticket.ArtifactView, same.ArtifactView)
	}
	ready, err := h.svc.UploadContent(ctx, h.alice, ticket.ID, strings.NewReader(body))
	h.must(err)
	if ready.Status != ArtifactReady || ready.SizeBytes != int64(len(body)) || len(ready.SHA256) != 64 || ready.RetentionDays != 30 {
		t.Fatalf("ready = %+v", ready)
	}
	again, err := h.svc.UploadContent(ctx, h.alice, ticket.ID, strings.NewReader("different"))
	h.must(err)
	if again.SHA256 != ready.SHA256 {
		t.Fatal("a second upload replaced a ready file")
	}
	if h.blobs.Len() != 1 {
		t.Fatalf("bodies = %d", h.blobs.Len())
	}

	// Uploads expire if nobody sends the bytes, and only members may upload.
	stale, err := init("late.csv", 4, "")
	h.must(err)
	h.clock.Advance(2 * time.Hour)
	if _, err = h.svc.UploadContent(ctx, h.alice, stale.ID, strings.NewReader("a\n1\n")); h.code(err) != "UPLOAD_EXPIRED" {
		t.Fatalf("late upload = %v", err)
	}
	if _, err = h.svc.InitUpload(ctx, h.bob, InitUploadInput{ProjectID: project.ID, Name: "x.csv", Size: 1}, key()); h.code(err) != "NOT_FOUND" {
		t.Fatalf("a stranger uploaded: %v", err)
	}
	if _, err = h.svc.UploadContent(ctx, h.bob, ticket.ID, strings.NewReader("x")); h.code(err) != "NOT_FOUND" {
		t.Fatalf("a stranger uploaded content: %v", err)
	}
}

func TestArtifactsAreOwnedPreviewedAndExpire(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	project := h.project(1_000_000)
	csv := h.upload(h.alice, project.ID, "orders.csv", "order_id,region\nA-1,east\nA-2,west\n")
	notes := h.upload(h.alice, project.ID, "notes.md", "# 标题\n正文\n")
	data := h.upload(h.alice, project.ID, "data.json", `{"b":1,"a":[1,2]}`)

	if _, err := h.svc.GetArtifact(ctx, h.bob, csv.ID); h.code(err) != "NOT_FOUND" {
		t.Fatalf("a stranger read metadata: %v", err)
	}
	if _, err := h.svc.OpenArtifact(ctx, h.bob, csv.ID); h.code(err) != "NOT_FOUND" {
		t.Fatalf("a stranger opened the file: %v", err)
	}
	if _, err := h.svc.PreviewArtifact(ctx, h.bob, csv.ID); h.code(err) != "NOT_FOUND" {
		t.Fatalf("a stranger previewed the file: %v", err)
	}
	if _, err := h.svc.GetArtifact(ctx, h.alice, "art_00000000000000000000000000000000"); h.code(err) != "NOT_FOUND" {
		t.Fatalf("missing artifact = %v", err)
	}

	table, err := h.svc.PreviewArtifact(ctx, h.alice, csv.ID)
	h.must(err)
	if table.Type != "table" || len(table.Columns) != 2 || len(table.Rows) != 2 || table.Rows[1][1] != "west" || table.Truncated {
		t.Fatalf("table preview = %+v", table)
	}
	text, err := h.svc.PreviewArtifact(ctx, h.alice, notes.ID)
	h.must(err)
	if text.Type != "text" || !strings.Contains(text.Text, "正文") {
		t.Fatalf("text preview = %+v", text)
	}
	pretty, err := h.svc.PreviewArtifact(ctx, h.alice, data.ID)
	h.must(err)
	if pretty.Type != "json" || !strings.Contains(pretty.Text, "\n  ") {
		t.Fatalf("json preview = %+v", pretty)
	}

	content, err := h.svc.OpenArtifact(ctx, h.alice, csv.ID)
	h.must(err)
	body, _ := io.ReadAll(content.Object)
	_ = content.Object.Close()
	if string(body) != "order_id,region\nA-1,east\nA-2,west\n" {
		t.Fatalf("body = %q", body)
	}

	// Signed links stand on their own, expire quickly and cannot be edited.
	link, err := h.svc.CreateDownloadLink(ctx, h.alice, csv.ID)
	h.must(err)
	// The service reports the path below the API root; the HTTP layer adds the mount it was served under.
	if !strings.HasPrefix(link.URL, "/download/") {
		t.Fatalf("link = %q", link.URL)
	}
	token := strings.TrimPrefix(link.URL, "/download/")
	if opened, err := h.svc.OpenByLink(ctx, token); err != nil {
		t.Fatal(err)
	} else {
		_ = opened.Object.Close()
	}
	payload, signature, _ := strings.Cut(token, ".")
	decoded, _ := base64.RawURLEncoding.DecodeString(payload)
	forged := base64.RawURLEncoding.EncodeToString(bytes.Replace(decoded, []byte(csv.ID), []byte(notes.ID), 1)) + "." + signature
	for name, bad := range map[string]string{"forged": forged, "no signature": payload, "garbage": "x.y", "empty": ""} {
		if _, err = h.svc.OpenByLink(ctx, bad); h.code(err) != "DOWNLOAD_LINK_INVALID" {
			t.Errorf("%s link: %v", name, err)
		}
	}
	if _, err = h.svc.CreateDownloadLink(ctx, h.bob, csv.ID); h.code(err) != "NOT_FOUND" {
		t.Fatalf("a stranger minted a link: %v", err)
	}
	h.clock.Advance(10 * time.Minute)
	if _, err = h.svc.OpenByLink(ctx, token); h.code(err) != "DOWNLOAD_LINK_INVALID" {
		t.Fatalf("an expired link worked: %v", err)
	}

	// After the retention period the body is deleted, the record stays as a tombstone.
	h.clock.Advance(31 * 24 * time.Hour)
	if _, err = h.svc.OpenArtifact(ctx, h.alice, csv.ID); h.code(err) != "ARTIFACT_EXPIRED" {
		t.Fatalf("open after retention = %v", err)
	}
	before := h.blobs.Len()
	removed, err := h.svc.expireArtifacts(ctx, 100)
	h.must(err)
	if removed != 3 || h.blobs.Len() != before-3 {
		t.Fatalf("removed %d, bodies %d -> %d", removed, before, h.blobs.Len())
	}
	view, err := h.svc.GetArtifact(ctx, h.alice, csv.ID)
	h.must(err)
	if view.Status != ArtifactExpired {
		t.Fatalf("status = %s", view.Status)
	}
	if again, err := h.svc.expireArtifacts(ctx, 100); err != nil || again != 0 {
		t.Fatalf("a second cleanup removed %d: %v", again, err)
	}
	listed, err := h.svc.ListArtifacts(ctx, h.alice, ListArtifactsQuery{ProjectID: project.ID, Kind: ArtifactInput})
	h.must(err)
	if len(listed.Items) != 3 {
		t.Fatalf("listed = %d", len(listed.Items))
	}
	if _, err = h.svc.ListArtifacts(ctx, h.alice, ListArtifactsQuery{ProjectID: project.ID, Kind: "weird"}); h.code(err) != "VALIDATION_ERROR" {
		t.Fatalf("bad kind = %v", err)
	}
}

func TestResultFilesSupersedeEachOtherButNeverTheInputs(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.publishPrices()
	project := h.project(10_000_000)
	input := h.runInput(project.ID, h.upload(h.alice, project.ID, "orders.csv", ordersOct))
	run := h.run(h.submit(input).ID)
	var stored Project
	h.must(h.db.Collection(colProjects).FindOne(ctx, bson.M{"_id": project.ID}).Decode(&stored))
	w, err := h.svc.openWorkspace(ctx, run, stored)
	h.must(err)

	first, err := w.write(ctx, "report.csv", []byte("v1\n"), "call_1", "first")
	h.must(err)
	second, err := w.write(ctx, "report.csv", []byte("v2 is longer\n"), "call_2", "second")
	h.must(err)
	if first.ID == second.ID {
		t.Fatal("results share an identity")
	}
	data, err := w.read(ctx, "report.csv")
	h.must(err)
	if string(data) != "v2 is longer\n" {
		t.Fatalf("read after overwrite = %q", data)
	}
	if _, err = w.write(ctx, "orders.csv", []byte("hacked"), "call_3", ""); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("an input was overwritten: %v", err)
	}
	if _, err = w.read(ctx, "missing.csv"); err == nil || !strings.Contains(err.Error(), "orders.csv") {
		t.Fatalf("the error should list available files: %v", err)
	}
	current, err := h.svc.ListArtifacts(ctx, h.alice, ListArtifactsQuery{ProjectID: project.ID, Kind: ArtifactOutput, RunID: run.ID})
	h.must(err)
	all, err := h.svc.ListArtifacts(ctx, h.alice, ListArtifactsQuery{ProjectID: project.ID, Kind: ArtifactOutput, RunID: run.ID, IncludeSuperseded: true})
	h.must(err)
	if len(current.Items) != 1 || current.Items[0].ID != second.ID || len(all.Items) != 2 {
		t.Fatalf("current %d, all %d", len(current.Items), len(all.Items))
	}
	rebuilt, err := h.svc.openWorkspace(ctx, run, stored)
	h.must(err)
	if artifact, _ := rebuilt.get("report.csv"); artifact.ID != second.ID {
		t.Fatalf("a rebuilt workspace saw %s, want %s", artifact.ID, second.ID)
	}
}

func TestSourceAndHTMLArtifactsHaveBoundedTextPreviews(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.publishPrices()
	project := h.project(10_000_000)
	run := h.run(h.submit(h.runInput(project.ID, h.upload(h.alice, project.ID, "orders.csv", ordersOct))).ID)
	var stored Project
	h.must(h.db.Collection(colProjects).FindOne(ctx, bson.M{"_id": project.ID}).Decode(&stored))
	w, err := h.svc.openWorkspace(ctx, run, stored)
	h.must(err)
	for name, body := range map[string]string{
		"analysis.PY": "print('中文')\n",
		"App.tsx":     "export default () => <h1>产物</h1>;\n",
		"config.yaml": "title: 中文\n",
		"report.html": "<h1>报告</h1><script>parent.alert('untrusted')</script>",
	} {
		t.Run(name, func(t *testing.T) {
			artifact, err := w.write(ctx, name, []byte(body), "call_preview", "")
			h.must(err)
			preview, err := h.svc.PreviewArtifact(ctx, h.alice, artifact.ID)
			h.must(err)
			if artifact.Type == "bin" || preview.Type != "text" || preview.Text != body || preview.Truncated {
				t.Fatalf("source or HTML could not be previewed: %+v", preview)
			}
			if _, err := h.svc.PreviewArtifact(ctx, h.bob, artifact.ID); h.code(err) != "NOT_FOUND" {
				t.Fatalf("a stranger previewed an output: %v", err)
			}
		})
	}
	long, err := w.write(ctx, "large.py", []byte(strings.Repeat("测", previewBytes)), "call_preview", "")
	h.must(err)
	preview, err := h.svc.PreviewArtifact(ctx, h.alice, long.ID)
	h.must(err)
	if !preview.Truncated || len(preview.Text) > previewBytes || !utf8.ValidString(preview.Text) || strings.ContainsRune(preview.Text, utf8.RuneError) {
		t.Fatalf("unbounded or corrupt source preview: %d bytes, truncated=%v", len(preview.Text), preview.Truncated)
	}
	binary, err := w.write(ctx, "result.bin", []byte{0, 255, 1}, "call_preview", "")
	h.must(err)
	preview, err = h.svc.PreviewArtifact(ctx, h.alice, binary.ID)
	h.must(err)
	if preview.Type != "binary" || preview.Text != "" {
		t.Fatalf("a binary artifact was treated as text: %+v", preview)
	}
}
