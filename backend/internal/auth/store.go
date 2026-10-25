package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/catalog"
	"agentlab.local/backend/internal/records"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	"golang.org/x/crypto/bcrypt"
)

const SessionLifetime = 12 * time.Hour

type User struct {
	access.Principal `bson:",inline"`
	Email            string `json:"email" bson:"email"`
	Status           string `json:"status" bson:"status"`
	Version          int    `json:"version" bson:"version"`
	CreatedAt        string `json:"createdAt" bson:"createdAt"`
	CanManage        bool   `json:"canManage" bson:"-"`
}
type userDoc struct {
	User         `bson:",inline"`
	PasswordHash string `bson:"passwordHash"`
}
type session struct {
	Hash      string    `bson:"_id"`
	UserID    string    `bson:"userId"`
	TenantID  string    `bson:"tenantId"`
	ExpiresAt time.Time `bson:"expiresAt"`
}
type Permissions struct {
	CatalogRead  []string `json:"catalogRead"`
	CatalogWrite bool     `json:"catalogWrite"`
	RecordsRead  []string `json:"recordsRead"`
	ManageUsers  bool     `json:"manageUsers"`
	Environment  bool     `json:"environment"`
}
type State struct {
	User        *User       `json:"user"`
	Permissions Permissions `json:"permissions"`
}
type RegisterInput struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Password    string `json:"password"`
}
type CreateInput struct {
	RegisterInput
	StudentID string `json:"studentId"`
	Role      string `json:"role"`
	Status    string `json:"status"`
}
type UpdateInput struct {
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	Version     int    `json:"version"`
}
type UserPage struct {
	Items    []User `json:"items"`
	Total    int64  `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"pageSize"`
}
type Response struct {
	State *State        `json:"state,omitempty"`
	User  *User         `json:"user,omitempty"`
	Page  *UserPage     `json:"page,omitempty"`
	Error *access.Error `json:"error,omitempty"`
}
type failureWindow struct {
	Count int
	Until time.Time
}
type Store struct {
	db        *mongo.Database
	tenant    string
	catalog   *catalog.Store
	mu        sync.Mutex
	failures  map[string]failureWindow
	dummyHash []byte
}

var usernamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{2,23}$`)

func NewStore(ctx context.Context, db *mongo.Database, tenant string, cat *catalog.Store) (*Store, error) {
	_, err := db.Collection("auth_users").Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "username", Value: 1}}, Options: options.Index().SetUnique(true).SetName("unique_username")},
		{Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "studentId", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"studentId": bson.M{"$type": "string"}}).SetName("unique_student_identity")},
	})
	if err != nil {
		return nil, err
	}
	_, err = db.Collection("auth_sessions").Indexes().CreateMany(ctx, []mongo.IndexModel{{Keys: bson.D{{Key: "expiresAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)}, {Keys: bson.D{{Key: "tenantId", Value: 1}, {Key: "userId", Value: 1}}}})
	if err != nil {
		return nil, err
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("not-a-real-account-password"), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	return &Store{db: db, tenant: tenant, catalog: cat, failures: map[string]failureWindow{}, dummyHash: dummy}, nil
}
func id() string {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic("cryptographic random source unavailable")
	}
	return hex.EncodeToString(data[:])
}
func hashToken(token string) string {
	value := sha256.Sum256([]byte(token))
	return hex.EncodeToString(value[:])
}
func failure(code, message string, status int) error {
	return &access.Error{Code: code, Message: message, Status: status}
}
func PublicError(err error) *access.Error {
	var known *access.Error
	if errors.As(err, &known) {
		return known
	}
	var validation *catalog.Error
	if errors.As(err, &validation) {
		return &access.Error{Code: validation.Code, Message: validation.Message, Fields: validation.Fields, Status: validation.Status}
	}
	return &access.Error{Code: "AUTH_UNAVAILABLE", Message: "账号服务暂时不可用，请稍后重试。", Status: 503}
}
func empty() []string { return []string{} }
func StateFor(user *User) State {
	permissions := Permissions{CatalogRead: empty(), RecordsRead: empty()}
	if user == nil {
		return State{Permissions: permissions}
	}
	for _, kind := range catalog.Kinds {
		if user.CatalogRead(kind) {
			permissions.CatalogRead = append(permissions.CatalogRead, kind)
		}
	}
	for _, kind := range records.Kinds {
		if user.RecordsRead(kind) {
			permissions.RecordsRead = append(permissions.RecordsRead, kind)
		}
	}
	permissions.CatalogWrite = user.CatalogWrite()
	permissions.ManageUsers = user.Manager()
	permissions.Environment = true
	return State{User: user, Permissions: permissions}
}

// LegacyRole maps accounts created before the two-role model. It is applied on
// read so existing local databases keep working until they are reseeded.
func LegacyRole(role string) string {
	switch role {
	case "owner", "admin", "teacher":
		return "admin"
	case "student", "guardian", "judge", "operator":
		return "user"
	default:
		return role
	}
}
func normalizeUser(u *User) { u.Role = LegacyRole(u.Role) }
func (s *Store) Resolve(ctx context.Context, token string) (User, error) {
	if len(token) != 64 {
		return User{}, access.Unauthenticated()
	}
	if _, err := hex.DecodeString(token); err != nil {
		return User{}, access.Unauthenticated()
	}
	var login session
	err := s.db.Collection("auth_sessions").FindOne(ctx, bson.M{"_id": hashToken(token), "tenantId": s.tenant, "expiresAt": bson.M{"$gt": time.Now()}}).Decode(&login)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return User{}, access.Unauthenticated()
	}
	if err != nil {
		return User{}, err
	}
	var value userDoc
	err = s.users().FindOne(ctx, bson.M{"_id": login.UserID, "tenantId": s.tenant, "status": "active"}).Decode(&value)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return User{}, access.Unauthenticated()
	}
	normalizeUser(&value.User)
	return value.User, err
}
func (s *Store) users() *mongo.Collection { return s.db.Collection("auth_users") }
func (s *Store) Login(ctx context.Context, username, password string) (User, string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !usernamePattern.MatchString(username) || len(password) > 72 {
		return User{}, "", failure("INVALID_CREDENTIALS", "用户名或密码不正确。", 401)
	}
	s.mu.Lock()
	window := s.failures[username]
	blocked := window.Count >= 8 && time.Now().Before(window.Until)
	s.mu.Unlock()
	if blocked {
		return User{}, "", failure("TOO_MANY_ATTEMPTS", "尝试次数过多，请稍后再登录。", 429)
	}
	var value userDoc
	err := s.users().FindOne(ctx, bson.M{"tenantId": s.tenant, "username": username}).Decode(&value)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return User{}, "", err
	}
	hash := s.dummyHash
	if err == nil {
		hash = []byte(value.PasswordHash)
	}
	valid := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil && err == nil && value.Status == "active"
	s.mu.Lock()
	if valid {
		delete(s.failures, username)
	} else {
		window = s.failures[username]
		if time.Now().After(window.Until) {
			window = failureWindow{Until: time.Now().Add(15 * time.Minute)}
		}
		window.Count++
		if len(s.failures) > 1024 {
			for key, v := range s.failures {
				if time.Now().After(v.Until) {
					delete(s.failures, key)
				}
			}
		}
		if len(s.failures) < 2048 {
			s.failures[username] = window
		}
	}
	s.mu.Unlock()
	if !valid {
		return User{}, "", failure("INVALID_CREDENTIALS", "用户名或密码不正确。", 401)
	}
	token := id()
	err = s.transaction(ctx, func(tx context.Context) error {
		// Serialize with password/role updates: credentials checked before a
		// concurrent reset must not create a fresh session after revocation.
		count, err := s.users().CountDocuments(tx, bson.M{"_id": value.ID, "tenantId": s.tenant, "version": value.Version, "status": "active"})
		if err != nil {
			return err
		}
		if count != 1 {
			return failure("INVALID_CREDENTIALS", "账号信息已更新，请重新登录。", 401)
		}
		_, err = s.db.Collection("auth_sessions").InsertOne(tx, session{Hash: hashToken(token), UserID: value.ID, TenantID: s.tenant, ExpiresAt: time.Now().Add(SessionLifetime)})
		return err
	})
	if err != nil {
		return User{}, "", err
	}
	normalizeUser(&value.User)
	return value.User, token, nil
}
func (s *Store) Logout(ctx context.Context, token string) error {
	_, err := s.db.Collection("auth_sessions").DeleteOne(ctx, bson.M{"_id": hashToken(token), "tenantId": s.tenant})
	return err
}
func validateRegistration(input *RegisterInput) error {
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.TrimSpace(input.Email)
	fields := map[string]string{}
	if !usernamePattern.MatchString(input.Username) {
		fields["username"] = "用户名为 3–24 位小写字母、数字、下划线或连字符，以字母开头。"
	}
	if utf8.RuneCountInString(input.DisplayName) < 1 || utf8.RuneCountInString(input.DisplayName) > 60 {
		fields["displayName"] = "姓名或昵称为 1–60 字。"
	}
	if utf8.RuneCountInString(input.Password) < 10 || len(input.Password) > 72 {
		fields["password"] = "密码至少 10 个字符，UTF-8 最多 72 字节。"
	}
	if input.Email != "" {
		address, err := mail.ParseAddress(input.Email)
		if err != nil || address.Address != input.Email || len(input.Email) > 254 {
			fields["email"] = "请输入正确的邮箱地址。"
		}
	}
	if len(fields) > 0 {
		return &access.Error{Code: "VALIDATION_ERROR", Message: "请检查注册信息。", Fields: fields, Status: 422}
	}
	return nil
}
func (s *Store) Register(ctx context.Context, input RegisterInput) (User, string, error) {
	user, err := s.create(access.System(ctx), CreateInput{RegisterInput: input, Role: "user", Status: "active"}, true)
	if err != nil {
		return User{}, "", err
	}
	return s.Login(ctx, user.Username, input.Password)
}
func (s *Store) CreateUser(ctx context.Context, input CreateInput) (User, error) {
	return s.create(ctx, input, false)
}
func (s *Store) create(ctx context.Context, input CreateInput, registration bool) (User, error) {
	p, err := (access.Scope{DB: s.db, Tenant: s.tenant}).Principal(ctx)
	if err != nil {
		return User{}, err
	}
	if !p.Manager() {
		return User{}, access.Denied()
	}
	if err := validateRegistration(&input.RegisterInput); err != nil {
		return User{}, err
	}
	if !slices.Contains(access.Roles, input.Role) || !slices.Contains([]string{"active", "disabled"}, input.Status) {
		return User{}, failure("VALIDATION_ERROR", "角色或账号状态不正确。", 422)
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	u := User{Principal: access.Principal{ID: id(), TenantID: s.tenant, Username: input.Username, DisplayName: input.DisplayName, Role: input.Role}, Email: input.Email, Status: input.Status, Version: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	createProfile := u.Role == "user" && input.StudentID == ""
	if u.Role == "user" {
		u.StudentID = input.StudentID
		if createProfile {
			u.StudentID = id()
		}
	} else if input.StudentID != "" {
		return User{}, failure("VALIDATION_ERROR", "只有用户账号可以绑定学员档案。", 422)
	}
	err = s.transaction(ctx, func(tx context.Context) error {
		if !createProfile && u.StudentID != "" {
			if _, err := s.catalog.Get(access.System(tx), "students", u.StudentID); err != nil {
				return err
			}
		}
		if createProfile {
			if err := s.catalog.CreateIdentityStudent(tx, u.StudentID, u.ID, u.Username, u.DisplayName, u.Email); err != nil {
				return err
			}
		}
		if _, err := s.users().InsertOne(tx, userDoc{User: u, PasswordHash: string(passwordHash)}); err != nil {
			return err
		}
		action, message := "auth.user.create", "创建登录账号"
		actor := p.ID
		if registration {
			action, message, actor = "auth.register", "注册用户账号", u.ID
		}
		return s.audit(tx, action, u, actor, message)
	})
	if mongo.IsDuplicateKeyError(err) {
		return User{}, failure("USERNAME_EXISTS", "用户名已被使用。", 409)
	}
	u.CanManage = s.canManage(p, u)
	return u, err
}
func (s *Store) transaction(ctx context.Context, fn func(context.Context) error) error {
	session, err := s.db.Client().StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		if _, err := s.db.Collection("catalog_guards").UpdateOne(tx, bson.M{"_id": s.tenant}, bson.M{"$inc": bson.M{"revision": 1}}); err != nil {
			return nil, err
		}
		return nil, fn(tx)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	return err
}
func (s *Store) audit(ctx context.Context, action string, u User, actor, message string) error {
	_, err := s.catalog.History().AppendAudit(ctx, id(), time.Now().UTC().Format(time.RFC3339Nano), records.Audit{Category: "security", Level: "info", ActorID: actor, Action: action, ResourceType: "users", ResourceID: u.ID, ResourceCode: u.Username, ResourceVersion: u.Version, Result: "success", RequestID: records.RequestID(ctx), Message: message})
	return err
}
func (s *Store) canManage(p access.Principal, u User) bool {
	return p.ID != u.ID && p.Manager()
}
func (s *Store) ListUsers(ctx context.Context, q string, page int) (UserPage, error) {
	p, err := (access.Scope{DB: s.db, Tenant: s.tenant}).Principal(ctx)
	result := UserPage{Items: []User{}, Page: page, PageSize: 20}
	if err != nil {
		return result, err
	}
	if !p.Manager() {
		return result, access.Denied()
	}
	if page < 1 || page > 100000 || len(q) > 150 {
		return result, failure("INVALID_QUERY", "账号查询条件不正确。", 400)
	}
	filter := bson.M{"tenantId": s.tenant}
	if q != "" {
		pattern := bson.Regex{Pattern: regexp.QuoteMeta(q), Options: "i"}
		filter["$or"] = bson.A{bson.M{"username": pattern}, bson.M{"displayName": pattern}}
	}
	count, err := s.users().CountDocuments(ctx, filter)
	if err != nil {
		return result, err
	}
	cur, err := s.users().Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetSkip(int64((page-1)*20)).SetLimit(20))
	if err != nil {
		return result, err
	}
	defer func() { _ = cur.Close(ctx) }()
	for cur.Next(ctx) {
		var doc userDoc
		if err := cur.Decode(&doc); err != nil {
			return result, err
		}
		normalizeUser(&doc.User)
		doc.CanManage = s.canManage(p, doc.User)
		result.Items = append(result.Items, doc.User)
	}
	result.Total = count
	return result, cur.Err()
}
func (s *Store) UpdateUser(ctx context.Context, userID string, input UpdateInput) (User, error) {
	p, err := (access.Scope{DB: s.db, Tenant: s.tenant}).Principal(ctx)
	if err != nil {
		return User{}, err
	}
	if !p.Manager() {
		return User{}, access.Denied()
	}
	var current userDoc
	if err := s.users().FindOne(ctx, bson.M{"tenantId": s.tenant, "_id": userID}).Decode(&current); err != nil {
		return User{}, failure("NOT_FOUND", "账号不存在。", 404)
	}
	if !s.canManage(p, current.User) {
		return User{}, access.Denied()
	}
	if !slices.Contains(access.Roles, input.Role) || !slices.Contains([]string{"active", "disabled"}, input.Status) || input.Version != current.Version {
		return User{}, failure("VERSION_CONFLICT", "账号信息已变化或状态不正确，请重新读取。", 409)
	}
	registration := RegisterInput{Username: current.Username, DisplayName: input.DisplayName, Email: input.Email, Password: "validation-placeholder"}
	if err := validateRegistration(&registration); err != nil {
		return User{}, err
	}
	u := current.User
	u.DisplayName = registration.DisplayName
	u.Email = registration.Email
	u.Role = input.Role
	u.Status = input.Status
	u.Version++
	u.CanManage = false
	createStudent := u.Role == "user" && u.StudentID == ""
	if createStudent {
		u.StudentID = id()
	}
	err = s.transaction(ctx, func(tx context.Context) error {
		if createStudent {
			if err := s.catalog.CreateIdentityStudent(tx, u.StudentID, u.ID, u.Username, u.DisplayName, u.Email); err != nil {
				return err
			}
		}
		result, err := s.users().ReplaceOne(tx, bson.M{"tenantId": s.tenant, "_id": userID, "version": input.Version}, userDoc{User: u, PasswordHash: current.PasswordHash})
		if err != nil {
			return err
		}
		if result.MatchedCount != 1 {
			return failure("VERSION_CONFLICT", "账号信息已更新，请重新读取。", 409)
		}
		if _, err := s.db.Collection("auth_sessions").DeleteMany(tx, bson.M{"tenantId": s.tenant, "userId": userID}); err != nil {
			return err
		}
		return s.audit(tx, "auth.user.update", u, p.ID, "更新账号角色或状态")
	})
	u.CanManage = s.canManage(p, u)
	return u, err
}
func (s *Store) ChangePassword(ctx context.Context, currentPassword, newPassword string) error {
	p, err := (access.Scope{DB: s.db, Tenant: s.tenant}).Principal(ctx)
	if err != nil {
		return err
	}
	var u userDoc
	if err := s.users().FindOne(ctx, bson.M{"_id": p.ID, "tenantId": s.tenant}).Decode(&u); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(currentPassword)) != nil {
		return failure("INVALID_CREDENTIALS", "当前密码不正确。", 422)
	}
	return s.replacePassword(ctx, u, newPassword, p.ID)
}
func (s *Store) ResetPassword(ctx context.Context, userID, password string) error {
	p, err := (access.Scope{DB: s.db, Tenant: s.tenant}).Principal(ctx)
	if err != nil {
		return err
	}
	var u userDoc
	if err := s.users().FindOne(ctx, bson.M{"_id": userID, "tenantId": s.tenant}).Decode(&u); err != nil {
		return failure("NOT_FOUND", "账号不存在。", 404)
	}
	if !s.canManage(p, u.User) {
		return access.Denied()
	}
	return s.replacePassword(ctx, u, password, p.ID)
}
func (s *Store) replacePassword(ctx context.Context, u userDoc, password, actor string) error {
	if utf8.RuneCountInString(password) < 10 || len(password) > 72 {
		return failure("VALIDATION_ERROR", "密码至少 10 个字符，UTF-8 最多 72 字节。", 422)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.transaction(ctx, func(tx context.Context) error {
		result, err := s.users().UpdateOne(tx, bson.M{"_id": u.ID, "tenantId": s.tenant, "version": u.Version}, bson.M{"$set": bson.M{"passwordHash": string(hash)}, "$inc": bson.M{"version": 1}})
		if err != nil {
			return err
		}
		if result.MatchedCount != 1 {
			return failure("VERSION_CONFLICT", "账号信息已更新，请重试。", 409)
		}
		if _, err := s.db.Collection("auth_sessions").DeleteMany(tx, bson.M{"tenantId": s.tenant, "userId": u.ID}); err != nil {
			return err
		}
		u.Version++
		return s.audit(tx, "auth.password.change", u.User, actor, "更新账号密码")
	})
}
