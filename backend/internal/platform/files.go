package platform

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"agentlab.local/backend/internal/platform/blob"
	"agentlab.local/backend/internal/platform/ids"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	uploadWindow    = time.Hour
	staleUploadLock = 15 * time.Minute
	maxNameBytes    = 120
	sniffBytes      = 1 << 20
	previewBytes    = 64 << 10
	previewRows     = 50
)

type fileClass struct {
	Type   string
	Media  string
	Text   bool
	Upload bool
}

// classes lists the formats the platform recognizes. Only some may be uploaded as inputs;
// results can be anything the sandbox produced and are classified the same way.
var classes = map[string]fileClass{
	".csv":  {"csv", "text/csv", true, true},
	".tsv":  {"tsv", "text/tab-separated-values", true, true},
	".json": {"json", "application/json", true, true},
	".txt":  {"txt", "text/plain", true, true},
	".md":   {"md", "text/markdown", true, true},
	".xlsx": {"xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", false, true},
	".png":  {"png", "image/png", false, false},
	".zip":  {"zip", "application/zip", false, false},
	".pdf":  {"pdf", "application/pdf", false, false},
	".html": {"html", "text/html", true, false},
	".py":   {"py", "text/plain", true, false},
	".js":   {"js", "text/plain", true, false},
	".mjs":  {"mjs", "text/plain", true, false},
	".cjs":  {"cjs", "text/plain", true, false},
	".ts":   {"ts", "text/plain", true, false},
	".tsx":  {"tsx", "text/plain", true, false},
	".jsx":  {"jsx", "text/plain", true, false},
	".go":   {"go", "text/plain", true, false},
	".java": {"java", "text/plain", true, false},
	".rs":   {"rs", "text/plain", true, false},
	".css":  {"css", "text/plain", true, false},
	".yaml": {"yaml", "text/plain", true, false},
	".yml":  {"yml", "text/plain", true, false},
	".toml": {"toml", "text/plain", true, false},
	".sql":  {"sql", "text/plain", true, false},
	".sh":   {"sh", "text/plain", true, false},
}

func classify(name string) fileClass {
	if class, ok := classes[strings.ToLower(path.Ext(name))]; ok {
		return class
	}
	return fileClass{Type: "bin", Media: "application/octet-stream"}
}

// sanitizeName turns a client-supplied file name into a safe workspace name: no directories,
// no characters that mean something to a shell or file system, a bounded length.
func sanitizeName(name string) (string, error) {
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	var cleaned strings.Builder
	for _, r := range base {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == ' ', r == '.', r == '_', r == '-', r == '(', r == ')', r == '+':
			cleaned.WriteRune(r)
		default:
			cleaned.WriteRune('_')
		}
	}
	result := strings.Trim(cleaned.String(), " .")
	if len(result) > maxNameBytes {
		ext := path.Ext(result)
		stem := strings.TrimSuffix(result, ext)
		if len(ext) > 16 {
			stem, ext = result, ""
		}
		result = cutBytes(stem, maxNameBytes-len(ext)) + ext
	}
	if result == "" || result == "_" || strings.HasPrefix(result, ".") {
		return "", invalidFields("文件名无效。", map[string]string{"name": "请提供带扩展名的文件名"})
	}
	return result, nil
}

// cutBytes shortens s to at most limit bytes without splitting a character.
func cutBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// InitUploadInput is the body of POST /v1/files.
type InitUploadInput struct {
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

// ArtifactView is the public shape of a stored file.
type ArtifactView struct {
	ID            string     `json:"id"`
	ProjectID     string     `json:"projectId"`
	Kind          string     `json:"kind"`
	Name          string     `json:"name"`
	Type          string     `json:"type"`
	MediaType     string     `json:"mediaType"`
	SizeBytes     int64      `json:"sizeBytes"`
	SHA256        string     `json:"sha256"`
	Status        string     `json:"status"`
	RunID         string     `json:"runId,omitempty"`
	ToolCallID    string     `json:"toolCallId,omitempty"`
	Summary       string     `json:"summary"`
	Superseded    bool       `json:"superseded"`
	RetentionDays int        `json:"retentionDays"`
	CreatedAt     time.Time  `json:"createdAt"`
	ReadyAt       *time.Time `json:"readyAt"`
	ExpiresAt     time.Time  `json:"expiresAt"`
}

func (s *Service) artifactView(a Artifact) ArtifactView {
	status := a.Status
	if status == ArtifactReady && !a.ExpiresAt.After(s.now()) {
		status = ArtifactExpired
	}
	size := a.Size
	days := int(a.ExpiresAt.Sub(a.CreatedAt).Hours() / 24)
	return ArtifactView{ID: a.ID, ProjectID: a.ProjectID, Kind: a.Kind, Name: a.Name, Type: a.Type, MediaType: a.MediaType, SizeBytes: size, SHA256: a.SHA256, Status: status, RunID: a.RunID, ToolCallID: a.ToolCallID, Summary: a.Summary, Superseded: a.SupersededBy != "", RetentionDays: days, CreatedAt: a.CreatedAt, ReadyAt: a.ReadyAt, ExpiresAt: a.ExpiresAt}
}

// UploadTicket tells the client where to send the bytes. The service reports the path relative to the
// API root; the HTTP layer prefixes the mount the request came through.
type UploadTicket struct {
	ArtifactView
	UploadURL       string    `json:"uploadUrl"`
	UploadExpiresAt time.Time `json:"uploadExpiresAt"`
}

func (s *Service) requireActiveProject(project Project) error {
	if project.Status != "active" {
		return problem("PROJECT_PAUSED", "项目已暂停，暂时不能提交运行或上传文件。", 409)
	}
	return nil
}

func (s *Service) canTouchFiles(actor Actor) bool {
	return actor.can(ScopeFilesWrite) || actor.can(ScopeArtifactsRead)
}

// InitUpload reserves a file id and returns where to PUT the content. Nothing is readable until
// the bytes arrive, match the declared size and pass validation.
func (s *Service) InitUpload(ctx context.Context, actor Actor, in InitUploadInput, idempotencyKey string) (UploadTicket, error) {
	granted, err := s.authorize(ctx, actor, in.ProjectID, ScopeFilesWrite, false)
	if err != nil {
		return UploadTicket{}, err
	}
	if err = s.requireActiveProject(granted.Project); err != nil {
		return UploadTicket{}, err
	}
	if err = requireIdempotencyKey(idempotencyKey); err != nil {
		return UploadTicket{}, err
	}
	fields := map[string]string{}
	name, nameErr := sanitizeName(in.Name)
	class := classify(name)
	switch {
	case nameErr != nil:
		fields["name"] = "请提供带扩展名的文件名"
	case !class.Upload:
		fields["name"] = "仅支持上传 csv、tsv、json、txt、md、xlsx 文件"
	}
	if in.Size < 1 || in.Size > s.cfg.MaxUploadBytes {
		fields["size"] = fmt.Sprintf("必须在 1 到 %d 字节之间", s.cfg.MaxUploadBytes)
	}
	if in.SHA256 != "" && !sha256Pattern.MatchString(in.SHA256) {
		fields["sha256"] = "必须是 64 位小写十六进制 SHA-256"
	}
	if len(fields) > 0 {
		return UploadTicket{}, invalidFields("请检查上传信息。", fields)
	}
	normalized := InitUploadInput{ProjectID: granted.Project.ID, Name: name, Size: in.Size, SHA256: in.SHA256}
	receipt, digest := receiptID(actor.TenantID, granted.Project.ID, actor, "file.init", idempotencyKey), digestOf(normalized)
	var artifact Artifact
	err = s.tx(ctx, func(tx context.Context) error {
		previous, e := s.findReceipt(tx, receipt, digest)
		if e != nil {
			return e
		}
		if previous != nil {
			return s.col(colArtifacts).FindOne(tx, bson.M{"_id": previous.ResourceID, "tenantId": actor.TenantID}).Decode(&artifact)
		}
		now := s.now()
		artifact = Artifact{ID: ids.New(ids.Artifact), TenantID: actor.TenantID, ProjectID: granted.Project.ID, Kind: ArtifactInput, Name: name, Type: class.Type, MediaType: class.Media, DeclaredSize: in.Size, SHA256: in.SHA256, Status: ArtifactPending, CreatedBy: actor.ID(), CreatedAt: now, ExpiresAt: now.Add(uploadWindow), Version: 1}
		artifact.BlobKey = artifact.ID
		if _, e = s.col(colArtifacts).InsertOne(tx, artifact); e != nil {
			return e
		}
		return s.putReceipt(tx, receipt, actor.TenantID, digest, "artifact", artifact.ID)
	})
	if err != nil {
		return UploadTicket{}, err
	}
	return UploadTicket{ArtifactView: s.artifactView(artifact), UploadURL: "/files/" + artifact.ID + "/content", UploadExpiresAt: artifact.ExpiresAt}, nil
}

func (s *Service) loadArtifact(ctx context.Context, tenant, id string) (Artifact, error) {
	var artifact Artifact
	err := s.col(colArtifacts).FindOne(ctx, bson.M{"_id": id, "tenantId": tenant}).Decode(&artifact)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return artifact, notFound()
	}
	return artifact, err
}

// textLooksValid checks a prefix of a text file. A prefix cut mid-character is not an error.
func textLooksValid(prefix []byte, complete bool) bool {
	if !complete {
		for i := 0; i < utf8.UTFMax && len(prefix) > 0 && !utf8.Valid(prefix); i++ {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return utf8.Valid(prefix)
}

// UploadContent receives the bytes of a pending upload. It is idempotent per file: once the
// upload succeeded, repeating it changes nothing.
func (s *Service) UploadContent(ctx context.Context, actor Actor, fileID string, body io.Reader) (ArtifactView, error) {
	artifact, err := s.loadArtifact(ctx, actor.TenantID, fileID)
	if err != nil {
		return ArtifactView{}, err
	}
	if artifact.Kind != ArtifactInput {
		return ArtifactView{}, notFound()
	}
	granted, err := s.authorize(ctx, actor, artifact.ProjectID, ScopeFilesWrite, false)
	if err != nil {
		return ArtifactView{}, err
	}
	if artifact.Status == ArtifactReady {
		return s.artifactView(artifact), nil
	}
	if artifact.Status != ArtifactPending || !artifact.ExpiresAt.After(s.now()) {
		return ArtifactView{}, problem("UPLOAD_EXPIRED", "上传已过期或已失败，请重新创建文件。", 410)
	}
	if err = s.requireActiveProject(granted.Project); err != nil {
		return ArtifactView{}, err
	}
	now := s.now()
	claimed, err := s.col(colArtifacts).UpdateOne(ctx, bson.M{"_id": artifact.ID, "status": ArtifactPending, "$or": bson.A{
		bson.M{"uploading": false},
		bson.M{"uploadStartedAt": bson.M{"$lt": now.Add(-staleUploadLock)}},
	}}, bson.M{"$set": bson.M{"uploading": true, "uploadStartedAt": now}})
	if err != nil {
		return ArtifactView{}, err
	}
	if claimed.MatchedCount != 1 {
		return ArtifactView{}, problem("UPLOAD_IN_PROGRESS", "此文件正在上传，请稍后查询状态。", 409)
	}
	release := func(failure error) (ArtifactView, error) {
		_, _ = s.col(colArtifacts).UpdateOne(context.WithoutCancel(ctx), bson.M{"_id": artifact.ID, "status": ArtifactPending}, bson.M{"$set": bson.M{"uploading": false}})
		return ArtifactView{}, failure
	}
	limit := artifact.DeclaredSize
	if limit > s.cfg.MaxUploadBytes {
		limit = s.cfg.MaxUploadBytes
	}
	// Only the holder of the upload claim writes, so a body left by an earlier failed attempt is garbage.
	if err = s.blobs.Delete(ctx, artifact.BlobKey); err != nil {
		return release(err)
	}
	info, err := s.blobs.Put(ctx, artifact.BlobKey, body, limit)
	switch {
	case errors.Is(err, blob.ErrTooLarge):
		return release(problem("FILE_TOO_LARGE", fmt.Sprintf("文件超过声明的大小 %d 字节。", limit), 413))
	case err != nil:
		return release(err)
	}
	reject := func(code, message string) (ArtifactView, error) {
		_ = s.blobs.Delete(context.WithoutCancel(ctx), artifact.BlobKey)
		_, _ = s.col(colArtifacts).UpdateOne(context.WithoutCancel(ctx), bson.M{"_id": artifact.ID, "status": ArtifactPending}, bson.M{"$set": bson.M{"status": ArtifactFailed, "uploading": false}})
		return ArtifactView{}, problem(code, message, 422)
	}
	if info.Size != artifact.DeclaredSize {
		return reject("SIZE_MISMATCH", fmt.Sprintf("收到 %d 字节，与声明的 %d 字节不符。", info.Size, artifact.DeclaredSize))
	}
	if artifact.SHA256 != "" && artifact.SHA256 != info.SHA256 {
		return reject("CHECKSUM_MISMATCH", "文件摘要与声明的 SHA-256 不符。")
	}
	if verdict := s.sniff(ctx, artifact); verdict != "" {
		return reject("INVALID_FILE", verdict)
	}
	ready := s.now()
	artifact.Status, artifact.Size, artifact.SHA256, artifact.ReadyAt = ArtifactReady, info.Size, info.SHA256, &ready
	artifact.ExpiresAt = ready.AddDate(0, 0, granted.Project.RetentionDays)
	result, err := s.col(colArtifacts).UpdateOne(ctx, bson.M{"_id": artifact.ID, "status": ArtifactPending}, bson.M{
		"$set": bson.M{"status": ArtifactReady, "size": info.Size, "sha256": info.SHA256, "readyAt": ready, "expiresAt": artifact.ExpiresAt, "uploading": false},
		"$inc": bson.M{"version": 1},
	})
	if err != nil {
		return release(err)
	}
	if result.MatchedCount != 1 {
		return ArtifactView{}, problem("UPLOAD_EXPIRED", "上传已过期或已失败，请重新创建文件。", 410)
	}
	return s.artifactView(artifact), nil
}

// sniff checks that a stored upload looks like what its extension claims. It returns a message
// for a problem and "" when the file is acceptable.
func (s *Service) sniff(ctx context.Context, artifact Artifact) string {
	object, err := s.blobs.Open(ctx, artifact.BlobKey)
	if err != nil {
		return "无法读取已上传的文件。"
	}
	defer func() { _ = object.Close() }()
	prefix, err := io.ReadAll(io.LimitReader(object, sniffBytes))
	if err != nil {
		return "无法读取已上传的文件。"
	}
	class := classify(artifact.Name)
	switch {
	case class.Text && !textLooksValid(prefix, int64(len(prefix)) >= artifact.DeclaredSize):
		return "文本文件必须是 UTF-8 编码。"
	case class.Type == "xlsx" && !bytes.HasPrefix(prefix, []byte("PK\x03\x04")):
		return "文件内容不是有效的 xlsx 工作簿。"
	}
	return ""
}

// GetArtifact returns the metadata of one file the caller may see.
func (s *Service) GetArtifact(ctx context.Context, actor Actor, id string) (ArtifactView, error) {
	artifact, _, err := s.artifactFor(ctx, actor, id)
	if err != nil {
		return ArtifactView{}, err
	}
	return s.artifactView(artifact), nil
}

func (s *Service) artifactFor(ctx context.Context, actor Actor, id string) (Artifact, projectAccess, error) {
	artifact, err := s.loadArtifact(ctx, actor.TenantID, id)
	if err != nil {
		return Artifact{}, projectAccess{}, err
	}
	granted, err := s.authorize(ctx, actor, artifact.ProjectID, "", false)
	if err != nil {
		return Artifact{}, projectAccess{}, err
	}
	if !s.canTouchFiles(actor) {
		return Artifact{}, projectAccess{}, problem("FORBIDDEN_SCOPE", "此 API 密钥没有读取文件所需的权限。", 403)
	}
	return artifact, granted, nil
}

// ListArtifactsQuery filters a project's files.
type ListArtifactsQuery struct {
	ProjectID         string
	Kind              string
	RunID             string
	IncludeSuperseded bool
	Cursor            string
	Limit             int
}

// ListArtifacts pages through a project's files, newest first.
func (s *Service) ListArtifacts(ctx context.Context, actor Actor, q ListArtifactsQuery) (Page[ArtifactView], error) {
	granted, err := s.authorize(ctx, actor, q.ProjectID, "", false)
	if err != nil {
		return Page[ArtifactView]{}, err
	}
	if !s.canTouchFiles(actor) {
		return Page[ArtifactView]{}, problem("FORBIDDEN_SCOPE", "此 API 密钥没有读取文件所需的权限。", 403)
	}
	base := bson.M{"tenantId": actor.TenantID, "projectId": granted.Project.ID, "status": bson.M{"$in": bson.A{ArtifactReady, ArtifactExpired}}}
	if q.Kind != "" {
		if q.Kind != ArtifactInput && q.Kind != ArtifactOutput {
			return Page[ArtifactView]{}, invalidFields("文件类型不正确。", map[string]string{"kind": "必须是 input 或 output"})
		}
		base["kind"] = q.Kind
	}
	if q.RunID != "" {
		base["runId"] = q.RunID
	}
	if !q.IncludeSuperseded {
		base["supersededBy"] = ""
	}
	page, err := findPage(ctx, s.col(colArtifacts), base, "createdAt", q.Cursor, q.Limit, func(a Artifact) (time.Time, string) { return a.CreatedAt, a.ID })
	if err != nil {
		return Page[ArtifactView]{}, err
	}
	out := Page[ArtifactView]{Items: make([]ArtifactView, len(page.Items)), NextCursor: page.NextCursor}
	for i, artifact := range page.Items {
		out.Items[i] = s.artifactView(artifact)
	}
	return out, nil
}

// ArtifactContent is an opened body with the metadata needed to serve it safely.
type ArtifactContent struct {
	Artifact Artifact
	Object   blob.Object
}

func (s *Service) openReady(ctx context.Context, artifact Artifact) (ArtifactContent, error) {
	if artifact.Status != ArtifactReady {
		return ArtifactContent{}, notFound()
	}
	if !artifact.ExpiresAt.After(s.now()) {
		return ArtifactContent{}, problem("ARTIFACT_EXPIRED", "文件已超过保留期，不再提供下载。", 410)
	}
	object, err := s.blobs.Open(ctx, artifact.BlobKey)
	if errors.Is(err, blob.ErrNotFound) {
		return ArtifactContent{}, problem("ARTIFACT_EXPIRED", "文件内容已被清理，不再提供下载。", 410)
	}
	if err != nil {
		return ArtifactContent{}, err
	}
	return ArtifactContent{Artifact: artifact, Object: object}, nil
}

// OpenArtifact opens a file for download after checking ownership. The caller must close Object.
func (s *Service) OpenArtifact(ctx context.Context, actor Actor, id string) (ArtifactContent, error) {
	artifact, _, err := s.artifactFor(ctx, actor, id)
	if err != nil {
		return ArtifactContent{}, err
	}
	return s.openReady(ctx, artifact)
}

// Preview is a server-rendered, bounded view of a file. It never contains markup to render: the
// client displays text and tables as text, and fetches images from the content endpoint.
type Preview struct {
	Type      string     `json:"type"`
	Name      string     `json:"name"`
	MediaType string     `json:"mediaType"`
	SizeBytes int64      `json:"sizeBytes"`
	Text      string     `json:"text,omitempty"`
	Columns   []string   `json:"columns,omitempty"`
	Rows      [][]string `json:"rows,omitempty"`
	Truncated bool       `json:"truncated"`
}

// PreviewArtifact renders a bounded preview: a table head for csv/tsv, formatted JSON, plain text.
func (s *Service) PreviewArtifact(ctx context.Context, actor Actor, id string) (Preview, error) {
	content, err := s.OpenArtifact(ctx, actor, id)
	if err != nil {
		return Preview{}, err
	}
	defer func() { _ = content.Object.Close() }()
	artifact := content.Artifact
	preview := Preview{Type: "binary", Name: artifact.Name, MediaType: artifact.MediaType, SizeBytes: artifact.Size}
	if artifact.Type == "png" {
		preview.Type = "image"
		return preview, nil
	}
	if !classify(artifact.Name).Text {
		return preview, nil
	}
	data, err := io.ReadAll(io.LimitReader(content.Object, sniffBytes))
	if err != nil {
		return Preview{}, err
	}
	preview.Truncated = int64(len(data)) < artifact.Size
	switch artifact.Type {
	case "csv", "tsv":
		preview.Type = "table"
		reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
		reader.FieldsPerRecord = -1
		if artifact.Type == "tsv" {
			reader.Comma = '\t'
		}
		if header, readErr := reader.Read(); readErr == nil {
			preview.Columns = header
			for len(preview.Rows) < previewRows {
				row, readErr := reader.Read()
				if readErr != nil {
					if !errors.Is(readErr, io.EOF) {
						preview.Truncated = true
					}
					break
				}
				preview.Rows = append(preview.Rows, row)
			}
			if len(preview.Rows) == previewRows {
				preview.Truncated = true
			}
		}
	case "json":
		preview.Type = "json"
		var pretty bytes.Buffer
		if !preview.Truncated && json.Indent(&pretty, data, "", "  ") == nil {
			data = pretty.Bytes()
		}
		preview.Text, preview.Truncated = cutText(data, previewBytes, preview.Truncated)
	default:
		preview.Type = "text"
		preview.Text, preview.Truncated = cutText(data, previewBytes, preview.Truncated)
	}
	return preview, nil
}

func cutText(data []byte, limit int, alreadyTruncated bool) (string, bool) {
	if len(data) <= limit {
		return strings.ToValidUTF8(string(data), "�"), alreadyTruncated
	}
	data = data[:limit]
	for len(data) > 0 && !utf8.Valid(data) {
		data = data[:len(data)-1]
	}
	return string(data), true
}

// ---- short-lived download links ----

type downloadClaim struct {
	Artifact string `json:"a"`
	Tenant   string `json:"t"`
	Expires  int64  `json:"e"`
}

// DownloadLink grants one artifact download for a few minutes without any other credential. Like
// UploadURL, the path is relative to the API root until the HTTP layer prefixes the mount.
type DownloadLink struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (s *Service) sign(payload []byte) string {
	mac := hmac.New(sha256.New, s.signing)
	_, _ = mac.Write([]byte("artifact-download-v1\x00"))
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// CreateDownloadLink returns a signed, expiring URL for a file the caller may read.
func (s *Service) CreateDownloadLink(ctx context.Context, actor Actor, id string) (DownloadLink, error) {
	artifact, _, err := s.artifactFor(ctx, actor, id)
	if err != nil {
		return DownloadLink{}, err
	}
	if artifact.Status != ArtifactReady || !artifact.ExpiresAt.After(s.now()) {
		return DownloadLink{}, problem("ARTIFACT_EXPIRED", "文件已超过保留期，不再提供下载。", 410)
	}
	expires := s.now().Add(s.cfg.DownloadURLTTL)
	payload, _ := json.Marshal(downloadClaim{Artifact: artifact.ID, Tenant: artifact.TenantID, Expires: expires.Unix()})
	token := base64.RawURLEncoding.EncodeToString(payload) + "." + s.sign(payload)
	return DownloadLink{URL: "/download/" + token, ExpiresAt: expires}, nil
}

// OpenByLink verifies a signed link and opens the file it names.
func (s *Service) OpenByLink(ctx context.Context, token string) (ArtifactContent, error) {
	denied := problem("DOWNLOAD_LINK_INVALID", "下载链接无效或已过期。", 403)
	encoded, signature, ok := strings.Cut(token, ".")
	if !ok {
		return ArtifactContent{}, denied
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || !hmac.Equal([]byte(signature), []byte(s.sign(payload))) {
		return ArtifactContent{}, denied
	}
	var claim downloadClaim
	if err = json.Unmarshal(payload, &claim); err != nil || claim.Expires <= s.now().Unix() {
		return ArtifactContent{}, denied
	}
	artifact, err := s.loadArtifact(ctx, claim.Tenant, claim.Artifact)
	if err != nil {
		return ArtifactContent{}, denied
	}
	return s.openReady(ctx, artifact)
}

// ---- retention ----

// expireArtifacts deletes the bodies of files past their retention (and abandoned uploads) and
// keeps the records as tombstones so run history stays complete. It is safe to run on many nodes.
func (s *Service) expireArtifacts(ctx context.Context, batch int) (int, error) {
	found, err := s.col(colArtifacts).Find(ctx, bson.M{"status": bson.M{"$in": bson.A{ArtifactReady, ArtifactPending}}, "expiresAt": bson.M{"$lte": s.now()}}, options.Find().SetLimit(int64(batch)))
	if err != nil {
		return 0, err
	}
	var expired []Artifact
	if err = found.All(ctx, &expired); err != nil {
		return 0, err
	}
	removed := 0
	for _, artifact := range expired {
		if err = s.blobs.Delete(ctx, artifact.BlobKey); err != nil {
			return removed, err
		}
		next := ArtifactExpired
		if artifact.Status == ArtifactPending {
			next = ArtifactFailed
		}
		result, err := s.col(colArtifacts).UpdateOne(ctx, bson.M{"_id": artifact.ID, "status": artifact.Status}, bson.M{"$set": bson.M{"status": next, "uploading": false}})
		if err != nil {
			return removed, err
		}
		removed += int(result.ModifiedCount)
	}
	return removed, nil
}

// ---- run workspace ----

// workspace is the file set one run can see: its uploaded inputs, which are read-only, and the
// latest result for each name it has produced. It is rebuilt from durable records, so a worker
// that takes over a run sees exactly what the previous one left.
type workspace struct {
	svc     *Service
	run     Run
	project Project
	files   map[string]Artifact
}

func (s *Service) openWorkspace(ctx context.Context, run Run, project Project) (*workspace, error) {
	w := &workspace{svc: s, run: run, project: project, files: map[string]Artifact{}}
	if len(run.WorkspaceArtifactIDs) > 0 {
		found, err := s.col(colArtifacts).Find(ctx, bson.M{"tenantId": run.TenantID, "projectId": run.ProjectID, "_id": bson.M{"$in": run.WorkspaceArtifactIDs}, "status": ArtifactReady, "expiresAt": bson.M{"$gt": s.now()}})
		if err != nil {
			return nil, err
		}
		var files []Artifact
		if err = found.All(ctx, &files); err != nil {
			return nil, err
		}
		if len(files) != len(run.WorkspaceArtifactIDs) {
			return nil, fmt.Errorf("conversation workspace file expired")
		}
		for _, file := range files {
			w.files[file.Name] = file
		}
	}
	if len(run.InputArtifactIDs) > 0 {
		found, err := s.col(colArtifacts).Find(ctx, bson.M{"tenantId": run.TenantID, "projectId": run.ProjectID, "_id": bson.M{"$in": run.InputArtifactIDs}})
		if err != nil {
			return nil, err
		}
		var inputs []Artifact
		if err = found.All(ctx, &inputs); err != nil {
			return nil, err
		}
		for _, artifact := range inputs {
			w.files[artifact.Name] = artifact
		}
	}
	found, err := s.col(colArtifacts).Find(ctx, bson.M{"tenantId": run.TenantID, "runId": run.ID, "kind": ArtifactOutput, "status": ArtifactReady, "supersededBy": ""})
	if err != nil {
		return nil, err
	}
	var outputs []Artifact
	if err = found.All(ctx, &outputs); err != nil {
		return nil, err
	}
	for _, artifact := range outputs {
		w.files[artifact.Name] = artifact
	}
	return w, nil
}

func (w *workspace) get(name string) (Artifact, bool) {
	artifact, ok := w.files[name]
	return artifact, ok
}

// names lists workspace files in a stable order.
func (w *workspace) names() []string {
	names := make([]string, 0, len(w.files))
	for name := range w.files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (w *workspace) read(ctx context.Context, name string) ([]byte, error) {
	artifact, ok := w.files[name]
	if !ok {
		return nil, fmt.Errorf("no file named %q; available files: %s", name, strings.Join(w.names(), ", "))
	}
	object, err := w.svc.blobs.Open(ctx, artifact.BlobKey)
	if err != nil {
		return nil, fmt.Errorf("file %q is no longer available", name)
	}
	defer func() { _ = object.Close() }()
	data, err := io.ReadAll(io.LimitReader(object, artifact.Size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != artifact.Size {
		return nil, fmt.Errorf("file %q changed in storage", name)
	}
	return data, nil
}

// errReadOnly marks an attempt to overwrite an uploaded input.
var errReadOnly = errors.New("input files are read-only")

// write stores a result file and supersedes an earlier result of the same name. The record is
// created pending first, so a crash can only leave an expiring pending record, never a ready
// record without a body.
func (w *workspace) write(ctx context.Context, name string, data []byte, toolCallID, summary string) (Artifact, error) {
	if existing, ok := w.files[name]; ok && existing.Kind == ArtifactInput {
		return Artifact{}, fmt.Errorf("%w: %q was uploaded by the customer", errReadOnly, name)
	}
	s := w.svc
	class := classify(name)
	now := s.now()
	artifact := Artifact{ID: ids.New(ids.Artifact), TenantID: w.run.TenantID, ProjectID: w.run.ProjectID, Kind: ArtifactOutput, Name: name, Type: class.Type, MediaType: class.Media, DeclaredSize: int64(len(data)), Status: ArtifactPending, RunID: w.run.ID, ToolCallID: toolCallID, Summary: summary, CreatedBy: w.run.ID, CreatedAt: now, ExpiresAt: now.Add(uploadWindow), Version: 1}
	artifact.BlobKey = artifact.ID
	if _, err := s.col(colArtifacts).InsertOne(ctx, artifact); err != nil {
		return Artifact{}, err
	}
	info, err := s.blobs.Put(ctx, artifact.BlobKey, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Artifact{}, err
	}
	ready := s.now()
	artifact.Status, artifact.Size, artifact.SHA256, artifact.ReadyAt = ArtifactReady, info.Size, info.SHA256, &ready
	artifact.ExpiresAt = ready.AddDate(0, 0, w.project.RetentionDays)
	err = s.tx(ctx, func(tx context.Context) error {
		if _, e := s.col(colArtifacts).UpdateMany(tx, bson.M{"tenantId": artifact.TenantID, "runId": w.run.ID, "kind": ArtifactOutput, "name": name, "supersededBy": "", "_id": bson.M{"$ne": artifact.ID}}, bson.M{"$set": bson.M{"supersededBy": artifact.ID}}); e != nil {
			return e
		}
		_, e := s.col(colArtifacts).UpdateOne(tx, bson.M{"_id": artifact.ID, "status": ArtifactPending}, bson.M{
			"$set": bson.M{"status": ArtifactReady, "size": info.Size, "sha256": info.SHA256, "readyAt": ready, "expiresAt": artifact.ExpiresAt},
			"$inc": bson.M{"version": 1},
		})
		return e
	})
	if err != nil {
		return Artifact{}, err
	}
	w.files[name] = artifact
	return artifact, nil
}
