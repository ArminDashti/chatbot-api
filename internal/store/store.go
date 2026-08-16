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

type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"display_name"`
	Role         string    `json:"role"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Conversation struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username,omitempty"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Role           string    `json:"role"`
	Body           string    `json:"body"`
	CreatedAt      time.Time `json:"created_at"`
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
	_, err := db.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, display_name, role)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (username) DO UPDATE SET
			password_hash = EXCLUDED.password_hash,
			display_name = EXCLUDED.display_name,
			role = EXCLUDED.role,
			updated_at = NOW()
	`, DefaultUsername, passwordHash, "Armin", RoleAdmin)
	return err
}

func GetUserByUsername(ctx context.Context, db *sql.DB, username string) (*User, error) {
	return scanUser(db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, display_name, role, created_at, updated_at
		FROM users WHERE username = $1
	`, username))
}

func GetUserByID(ctx context.Context, db *sql.DB, id string) (*User, error) {
	return scanUser(db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, display_name, role, created_at, updated_at
		FROM users WHERE id = $1
	`, id))
}

func ListUsers(ctx context.Context, db *sql.DB) ([]User, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, username, password_hash, display_name, role, created_at, updated_at
		FROM users ORDER BY username
	`)
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

func ListConversationsForUser(ctx context.Context, db *sql.DB, userID string) ([]Conversation, error) {
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

func ChatStats(ctx context.Context, db *sql.DB) (*ChatStats, error) {
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
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func scanUserRow(rows *sql.Rows) (*User, error) {
	var u User
	err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}
