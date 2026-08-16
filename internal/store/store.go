package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	DefaultUsername = "armin"
	DefaultPassword = "dopadopa123"
	RoleAdmin       = "admin"
	RoleUser        = "user"
)

var ErrLastAdmin = fmt.Errorf("last admin")

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"display_name"`
	Role         string    `json:"role"`
	GroupID      string    `json:"group_id"`
	GroupName    string    `json:"group_name"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Conversation struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username,omitempty"`
	Title     string    `json:"title"`
	Device    string    `json:"device"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Role           string    `json:"role"`
	Body           string    `json:"body"`
	CreatedAt      time.Time `json:"created_at"`
	Feedback       *string   `json:"feedback,omitempty"`
}

type Group struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	RuleBody  string    `json:"rule_body"`
	MemberIDs []string  `json:"member_ids"`
	CreatedAt time.Time `json:"created_at"`
}

type SummaryStats struct {
	Users         int `json:"users"`
	Conversations int `json:"conversations"`
	Messages      int `json:"messages"`
}

type UserChatStat struct {
	UserID        string `json:"user_id"`
	Username      string `json:"username"`
	Conversations int    `json:"conversations"`
	Messages      int    `json:"messages"`
}

type DayChatStat struct {
	Day           string `json:"day"`
	Conversations int    `json:"conversations"`
	Messages      int    `json:"messages"`
}

type ChatStats struct {
	ByUser []UserChatStat `json:"by_user"`
	ByDay  []DayChatStat  `json:"by_day"`
}

func Open(databaseURL string) (*sql.DB, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return db, nil
}

func Migrate(db *sql.DB, migrationsDir string) error {
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for _, name := range files {
		body, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

func SeedDefaultUser(ctx context.Context, db *sql.DB, passwordHash string) error {
	gid, err := DefaultGroupID(ctx, db)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, display_name, role, group_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (username) DO UPDATE SET
			password_hash = EXCLUDED.password_hash,
			display_name = EXCLUDED.display_name,
			role = EXCLUDED.role,
			group_id = EXCLUDED.group_id,
			updated_at = NOW()
	`, DefaultUsername, passwordHash, "Armin", RoleAdmin, gid)
	if err != nil {
		return err
	}
	u, err := GetUserByUsername(ctx, db, DefaultUsername)
	if err != nil {
		return err
	}
	return SetUserGroup(ctx, db, u.ID, gid)
}

func DefaultGroupID(ctx context.Context, db *sql.DB) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `SELECT id FROM groups WHERE name = 'Default' LIMIT 1`).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	err = db.QueryRowContext(ctx, `
		INSERT INTO groups (name) VALUES ('Default')
		ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		RETURNING id
	`).Scan(&id)
	if err != nil {
		return "", err
	}
	_, _ = db.ExecContext(ctx, `
		INSERT INTO group_rules (group_id, body)
		VALUES ($1, '')
		ON CONFLICT (group_id) DO NOTHING
	`, id)
	return id, nil
}

func SetUserGroup(ctx context.Context, db *sql.DB, userID, groupID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET group_id = $1, updated_at = NOW() WHERE id = $2`, groupID, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM group_members WHERE user_id = $1`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`, groupID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func userSelect() string {
	return `
		SELECT u.id, u.username, u.password_hash, u.display_name, u.role, u.group_id, g.name, u.created_at, u.updated_at
		FROM users u
		JOIN groups g ON g.id = u.group_id
	`
}

func GetUserByUsername(ctx context.Context, db *sql.DB, username string) (*User, error) {
	return scanUser(db.QueryRowContext(ctx, userSelect()+` WHERE u.username = $1`, username))
}

func GetUserByID(ctx context.Context, db *sql.DB, id string) (*User, error) {
	return scanUser(db.QueryRowContext(ctx, userSelect()+` WHERE u.id = $1`, id))
}

func ListUsers(ctx context.Context, db *sql.DB) ([]User, error) {
	rows, err := db.QueryContext(ctx, userSelect()+` ORDER BY u.username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]User, 0)
	for rows.Next() {
		u, err := scanUserRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func UpdateUser(ctx context.Context, db *sql.DB, id, displayName, passwordHash string) (*User, error) {
	if passwordHash != "" {
		_, err := db.ExecContext(ctx, `
			UPDATE users SET display_name = $2, password_hash = $3, updated_at = NOW()
			WHERE id = $1
		`, id, displayName, passwordHash)
		if err != nil {
			return nil, err
		}
	} else {
		_, err := db.ExecContext(ctx, `
			UPDATE users SET display_name = $2, updated_at = NOW()
			WHERE id = $1
		`, id, displayName)
		if err != nil {
			return nil, err
		}
	}
	return GetUserByID(ctx, db, id)
}

func CreateConversation(ctx context.Context, db *sql.DB, userID, title string) (*Conversation, error) {
	if strings.TrimSpace(title) == "" {
		title = "New chat"
	}
	var conv Conversation
	err := db.QueryRowContext(ctx, `
		INSERT INTO conversations (user_id, title)
		VALUES ($1, $2)
		RETURNING id, user_id, title, created_at, updated_at
	`, userID, title).Scan(&conv.ID, &conv.UserID, &conv.Title, &conv.CreatedAt, &conv.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &conv, nil
}

func ListConversationsForUser(ctx context.Context, db *sql.DB, userID, query string) ([]Conversation, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		rows, err := db.QueryContext(ctx, `
			SELECT id, user_id, title, created_at, updated_at
			FROM conversations
			WHERE user_id = $1
			ORDER BY updated_at DESC
		`, userID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanConversations(rows)
	}
	like := "%" + query + "%"
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT c.id, c.user_id, c.title, c.created_at, c.updated_at
		FROM conversations c
		LEFT JOIN messages m ON m.conversation_id = c.id
		WHERE c.user_id = $1
			AND (c.title ILIKE $2 OR m.body ILIKE $2)
		ORDER BY c.updated_at DESC
	`, userID, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanConversations(rows)
}

func DeleteConversationForUser(ctx context.Context, db *sql.DB, conversationID, userID string) error {
	res, err := db.ExecContext(ctx, `
		DELETE FROM conversations WHERE id = $1 AND user_id = $2
	`, conversationID, userID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

type KnowledgeChunk struct {
	RelPath string
	Heading string
	Body    string
}

func ReplaceKnowledgeChunks(ctx context.Context, db *sql.DB, chunks []KnowledgeChunk) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_chunks`); err != nil {
		return 0, err
	}
	for _, chunk := range chunks {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO knowledge_chunks (rel_path, heading, body)
			VALUES ($1, $2, $3)
		`, chunk.RelPath, chunk.Heading, chunk.Body); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(chunks), nil
}

func SearchKnowledgeChunks(ctx context.Context, db *sql.DB, query string, limit int) ([]KnowledgeChunk, error) {
	query = strings.TrimSpace(query)
	if query == "" || limit <= 0 {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT rel_path, heading, body
		FROM knowledge_chunks
		WHERE tsv @@ plainto_tsquery('simple', $1)
			OR heading ILIKE $2
			OR body ILIKE $2
		ORDER BY ts_rank(tsv, plainto_tsquery('simple', $1)) DESC
		LIMIT $3
	`, query, "%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]KnowledgeChunk, 0)
	for rows.Next() {
		var chunk KnowledgeChunk
		if err := rows.Scan(&chunk.RelPath, &chunk.Heading, &chunk.Body); err != nil {
			return nil, err
		}
		out = append(out, chunk)
	}
	return out, rows.Err()
}

func FormatKnowledgeHits(chunks []KnowledgeChunk) string {
	var b strings.Builder
	for _, chunk := range chunks {
		b.WriteString("Source: ")
		b.WriteString(chunk.RelPath)
		if strings.TrimSpace(chunk.Heading) != "" {
			b.WriteString(" — ")
			b.WriteString(chunk.Heading)
		}
		b.WriteString("\n")
		b.WriteString(chunk.Body)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

func CreateUser(ctx context.Context, db *sql.DB, username, passwordHash, displayName, role string) (*User, error) {
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	if role != RoleAdmin {
		role = RoleUser
	}
	gid, err := DefaultGroupID(ctx, db)
	if err != nil {
		return nil, err
	}
	var id string
	err = db.QueryRowContext(ctx, `
		INSERT INTO users (username, password_hash, display_name, role, group_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, username, passwordHash, displayName, role, gid).Scan(&id)
	if err != nil {
		return nil, err
	}
	if err := SetUserGroup(ctx, db, id, gid); err != nil {
		return nil, err
	}
	return GetUserByID(ctx, db, id)
}

func AdminUpdateUser(ctx context.Context, db *sql.DB, id, username, displayName, role, passwordHash string) (*User, error) {
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	if role != RoleAdmin {
		role = RoleUser
	}
	current, err := GetUserByID(ctx, db, id)
	if err != nil {
		return nil, err
	}
	if current.Role == RoleAdmin && role != RoleAdmin {
		ok, err := hasOtherAdmin(ctx, db, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrLastAdmin
		}
	}
	if passwordHash != "" {
		_, err = db.ExecContext(ctx, `
			UPDATE users
			SET username = $2, display_name = $3, role = $4, password_hash = $5, updated_at = NOW()
			WHERE id = $1
		`, id, username, displayName, role, passwordHash)
	} else {
		_, err = db.ExecContext(ctx, `
			UPDATE users
			SET username = $2, display_name = $3, role = $4, updated_at = NOW()
			WHERE id = $1
		`, id, username, displayName, role)
	}
	if err != nil {
		return nil, err
	}
	return GetUserByID(ctx, db, id)
}

func DeleteUser(ctx context.Context, db *sql.DB, id string) error {
	current, err := GetUserByID(ctx, db, id)
	if err != nil {
		return err
	}
	if current.Role == RoleAdmin {
		ok, err := hasOtherAdmin(ctx, db, id)
		if err != nil {
			return err
		}
		if !ok {
			return ErrLastAdmin
		}
	}
	res, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func hasOtherAdmin(ctx context.Context, db *sql.DB, exceptID string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM users WHERE role = $1 AND id <> $2
	`, RoleAdmin, exceptID).Scan(&n)
	return n > 0, err
}

func GetConversation(ctx context.Context, db *sql.DB, id string) (*Conversation, error) {
	var conv Conversation
	err := db.QueryRowContext(ctx, `
		SELECT id, user_id, title, created_at, updated_at
		FROM conversations WHERE id = $1
	`, id).Scan(&conv.ID, &conv.UserID, &conv.Title, &conv.CreatedAt, &conv.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &conv, nil
}

func ListMessages(ctx context.Context, db *sql.DB, conversationID string) ([]Message, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, conversation_id, role, body, created_at
		FROM messages
		WHERE conversation_id = $1
		ORDER BY created_at ASC
	`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Message, 0)
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Body, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func InsertMessage(ctx context.Context, db *sql.DB, conversationID, role, body string) (*Message, error) {
	var m Message
	err := db.QueryRowContext(ctx, `
		INSERT INTO messages (conversation_id, role, body)
		VALUES ($1, $2, $3)
		RETURNING id, conversation_id, role, body, created_at
	`, conversationID, role, body).Scan(&m.ID, &m.ConversationID, &m.Role, &m.Body, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	_, _ = db.ExecContext(ctx, `UPDATE conversations SET updated_at = NOW() WHERE id = $1`, conversationID)
	return &m, nil
}

func TouchConversationTitle(ctx context.Context, db *sql.DB, id, title string) error {
	_, err := db.ExecContext(ctx, `UPDATE conversations SET title = $2, updated_at = NOW() WHERE id = $1`, id, title)
	return err
}

type ChatSettings struct {
	BaseURL         string
	APIKey          string
	Model           string
	AllowedFolders  []string
}

func splitStoredGuidePaths(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	parts := strings.Split(raw, "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func joinStoredGuidePaths(paths []string) string {
	return strings.Join(paths, "\n")
}

func GetChatSettings(ctx context.Context, db *sql.DB) (*ChatSettings, error) {
	var s ChatSettings
	var folderBlob string
	err := db.QueryRowContext(ctx, `
		SELECT chat_base_url, chat_api_key, chat_model,
			COALESCE(array_to_string(allowed_folders, E'\n'), '')
		FROM app_settings WHERE id = 1
	`).Scan(&s.BaseURL, &s.APIKey, &s.Model, &folderBlob)
	if err == sql.ErrNoRows {
		return &ChatSettings{Model: "auto", AllowedFolders: []string{}}, nil
	}
	if err != nil {
		return nil, err
	}
	s.AllowedFolders = splitStoredGuidePaths(folderBlob)
	return &s, nil
}

func PutChatSettings(ctx context.Context, db *sql.DB, baseURL, model, apiKey string, updateKey bool, allowedFolders []string) error {
	folderBlob := joinStoredGuidePaths(allowedFolders)
	if updateKey {
		_, err := db.ExecContext(ctx, `
			INSERT INTO app_settings (id, chat_base_url, chat_model, chat_api_key, allowed_folders, updated_at)
			VALUES (1, $1, $2, $3, CASE WHEN $4 = '' THEN ARRAY[]::TEXT[] ELSE string_to_array($4, E'\n') END, NOW())
			ON CONFLICT (id) DO UPDATE SET
				chat_base_url = EXCLUDED.chat_base_url,
				chat_model = EXCLUDED.chat_model,
				chat_api_key = EXCLUDED.chat_api_key,
				allowed_folders = EXCLUDED.allowed_folders,
				updated_at = NOW()
		`, baseURL, model, apiKey, folderBlob)
		return err
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO app_settings (id, chat_base_url, chat_model, allowed_folders, updated_at)
		VALUES (1, $1, $2, CASE WHEN $3 = '' THEN ARRAY[]::TEXT[] ELSE string_to_array($3, E'\n') END, NOW())
		ON CONFLICT (id) DO UPDATE SET
			chat_base_url = EXCLUDED.chat_base_url,
			chat_model = EXCLUDED.chat_model,
			allowed_folders = EXCLUDED.allowed_folders,
			updated_at = NOW()
	`, baseURL, model, folderBlob)
	return err
}

func APIKeyHint(key string) string {
	if strings.TrimSpace(key) == "" {
		return ""
	}
	return "**********"
}

func GetGlobalRule(ctx context.Context, db *sql.DB) (string, error) {
	var body string
	err := db.QueryRowContext(ctx, `SELECT body FROM global_rules WHERE id = 1`).Scan(&body)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return body, err
}

func PutGlobalRule(ctx context.Context, db *sql.DB, body string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO global_rules (id, body, updated_at)
		VALUES (1, $1, NOW())
		ON CONFLICT (id) DO UPDATE SET body = EXCLUDED.body, updated_at = NOW()
	`, body)
	return err
}

func RulesForUser(ctx context.Context, db *sql.DB, userID string) (string, error) {
	global, err := GetGlobalRule(ctx, db)
	if err != nil {
		return "", err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT COALESCE(gr.body, '')
		FROM group_members gm
		JOIN group_rules gr ON gr.group_id = gm.group_id
		WHERE gm.user_id = $1 AND COALESCE(gr.body, '') <> ''
	`, userID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	if strings.TrimSpace(global) != "" {
		b.WriteString(strings.TrimSpace(global))
		b.WriteString("\n\n")
	}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return "", err
		}
		b.WriteString(strings.TrimSpace(body))
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String()), rows.Err()
}

func CreateGroup(ctx context.Context, db *sql.DB, name string, memberIDs []string) (*Group, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var g Group
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO groups (name) VALUES ($1)
		RETURNING id, name, created_at
	`, name).Scan(&g.ID, &g.Name, &g.CreatedAt); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO group_rules (group_id, body) VALUES ($1, '')`, g.ID); err != nil {
		return nil, err
	}
	for _, uid := range memberIDs {
		uid = strings.TrimSpace(uid)
		if uid == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, g.ID, uid); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return GetGroup(ctx, db, g.ID)
}

func ListGroups(ctx context.Context, db *sql.DB) ([]Group, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT g.id, g.name, g.created_at, COALESCE(r.body, '')
		FROM groups g
		LEFT JOIN group_rules r ON r.group_id = g.id
		ORDER BY g.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Group, 0)
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedAt, &g.RuleBody); err != nil {
			return nil, err
		}
		members, err := groupMemberIDs(ctx, db, g.ID)
		if err != nil {
			return nil, err
		}
		g.MemberIDs = members
		out = append(out, g)
	}
	return out, rows.Err()
}

func GetGroup(ctx context.Context, db *sql.DB, id string) (*Group, error) {
	var g Group
	err := db.QueryRowContext(ctx, `
		SELECT g.id, g.name, g.created_at, COALESCE(r.body, '')
		FROM groups g
		LEFT JOIN group_rules r ON r.group_id = g.id
		WHERE g.id = $1
	`, id).Scan(&g.ID, &g.Name, &g.CreatedAt, &g.RuleBody)
	if err != nil {
		return nil, err
	}
	members, err := groupMemberIDs(ctx, db, g.ID)
	if err != nil {
		return nil, err
	}
	g.MemberIDs = members
	return &g, nil
}

func PutGroupRule(ctx context.Context, db *sql.DB, groupID, body string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO group_rules (group_id, body, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (group_id) DO UPDATE SET body = EXCLUDED.body, updated_at = NOW()
	`, groupID, body)
	return err
}

func PutGroupMembers(ctx context.Context, db *sql.DB, groupID string, memberIDs []string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM group_members WHERE group_id = $1`, groupID); err != nil {
		return err
	}
	for _, uid := range memberIDs {
		uid = strings.TrimSpace(uid)
		if uid == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)
		`, groupID, uid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func AdminListConversations(ctx context.Context, db *sql.DB, userID string) ([]Conversation, error) {
	q := `
		SELECT c.id, c.user_id, u.username, c.title, c.created_at, c.updated_at
		FROM conversations c
		JOIN users u ON u.id = c.user_id
	`
	args := []any{}
	if strings.TrimSpace(userID) != "" {
		q += ` WHERE c.user_id = $1`
		args = append(args, userID)
	}
	q += ` ORDER BY c.updated_at DESC`
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Conversation, 0)
	for rows.Next() {
		var conv Conversation
		if err := rows.Scan(&conv.ID, &conv.UserID, &conv.Username, &conv.Title, &conv.CreatedAt, &conv.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, conv)
	}
	return out, rows.Err()
}

func Summary(ctx context.Context, db *sql.DB) (*SummaryStats, error) {
	var s SummaryStats
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&s.Users); err != nil {
		return nil, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversations`).Scan(&s.Conversations); err != nil {
		return nil, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&s.Messages); err != nil {
		return nil, err
	}
	return &s, nil
}

func GetChatStats(ctx context.Context, db *sql.DB) (*ChatStats, error) {
	out := &ChatStats{ByUser: []UserChatStat{}, ByDay: []DayChatStat{}}
	rows, err := db.QueryContext(ctx, `
		SELECT u.id, u.username,
			(SELECT COUNT(*) FROM conversations c WHERE c.user_id = u.id),
			(SELECT COUNT(*) FROM messages m JOIN conversations c ON c.id = m.conversation_id WHERE c.user_id = u.id)
		FROM users u
		ORDER BY u.username
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s UserChatStat
		if err := rows.Scan(&s.UserID, &s.Username, &s.Conversations, &s.Messages); err != nil {
			return nil, err
		}
		out.ByUser = append(out.ByUser, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	dayRows, err := db.QueryContext(ctx, `
		SELECT to_char(m.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS day,
			COUNT(DISTINCT m.conversation_id),
			COUNT(*)
		FROM messages m
		GROUP BY day
		ORDER BY day DESC
		LIMIT 30
	`)
	if err != nil {
		return nil, err
	}
	defer dayRows.Close()
	for dayRows.Next() {
		var s DayChatStat
		if err := dayRows.Scan(&s.Day, &s.Conversations, &s.Messages); err != nil {
			return nil, err
		}
		out.ByDay = append(out.ByDay, s)
	}
	return out, dayRows.Err()
}

func groupMemberIDs(ctx context.Context, db *sql.DB, groupID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT user_id FROM group_members WHERE group_id = $1`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func scanConversations(rows *sql.Rows) ([]Conversation, error) {
	out := make([]Conversation, 0)
	for rows.Next() {
		var conv Conversation
		if err := rows.Scan(&conv.ID, &conv.UserID, &conv.Title, &conv.CreatedAt, &conv.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, conv)
	}
	return out, rows.Err()
}

func scanUser(row *sql.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.GroupID, &u.GroupName, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func scanUserRow(rows *sql.Rows) (*User, error) {
	var u User
	err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.GroupID, &u.GroupName, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}
