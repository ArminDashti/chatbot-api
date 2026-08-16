package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ArminDashti/chatbot-api/internal/auth"
	"github.com/ArminDashti/chatbot-api/internal/config"
	"github.com/ArminDashti/chatbot-api/internal/knowledge"
	"github.com/ArminDashti/chatbot-api/internal/llm"
	"github.com/ArminDashti/chatbot-api/internal/store"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

type Server struct {
	cfg config.Config
	db  *sql.DB
	llm *llm.Client
}

func New(cfg config.Config, db *sql.DB, chat *llm.Client) *Server {
	return &Server{cfg: cfg, db: db, llm: chat}
}

func (s *Server) Router() *gin.Engine {
	r := gin.Default()
	allow := map[string]struct{}{}
	for _, o := range s.cfg.CORSOrigins {
		allow[o] = struct{}{}
	}
	r.Use(cors.New(cors.Config{
		AllowOriginFunc: func(origin string) bool {
			if len(allow) == 0 {
				return true
			}
			_, ok := allow[origin]
			return ok
		},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}))

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api/v1")
	{
		api.POST("/auth/login", s.login)

		authed := api.Group("")
		authed.Use(auth.Middleware(s.cfg.JWTSecret))
		{
			authed.GET("/me", s.getMe)
			authed.PATCH("/me", s.patchMe)
			authed.GET("/chat/ready", s.chatReady)
			authed.POST("/conversations", s.createConversation)
			authed.GET("/conversations", s.listConversations)
			authed.GET("/conversations/:id", s.getConversation)
			authed.DELETE("/conversations/:id", s.deleteConversation)
			authed.POST("/conversations/:id/messages", s.postMessage)

			admin := authed.Group("/admin")
			admin.Use(auth.RequireAdmin())
			{
				admin.GET("/stats/summary", s.adminSummary)
				admin.GET("/stats/chats", s.adminChatStats)
				admin.GET("/conversations", s.adminConversations)
				admin.GET("/conversations/:id", s.adminConversation)
				admin.GET("/rules", s.getGlobalRule)
				admin.PUT("/rules", s.putGlobalRule)
				admin.GET("/groups", s.listGroups)
				admin.POST("/groups", s.createGroup)
				admin.PUT("/groups/:id/rules", s.putGroupRule)
				admin.PUT("/groups/:id/members", s.putGroupMembers)
				admin.GET("/users", s.listUsers)
				admin.POST("/users", s.createUser)
				admin.PATCH("/users/:id", s.patchUser)
				admin.DELETE("/users/:id", s.deleteUser)
				admin.POST("/knowledge/reindex", s.reindexKnowledge)
				admin.GET("/settings", s.getSettings)
				admin.PUT("/settings", s.putSettings)
			}
		}
	}
	return r
}

func writeError(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"error": msg})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password" binding:"required"`
}

func (s *Server) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request")
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		writeError(c, http.StatusBadRequest, "username is required")
		return
	}
	user, err := store.GetUserByUsername(c.Request.Context(), s.db, username)
	if err == sql.ErrNoRows {
		writeError(c, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not load user")
		return
	}
	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		writeError(c, http.StatusUnauthorized, "invalid username or password")
		return
	}
	token, err := auth.IssueToken(s.cfg.JWTSecret, user.ID, user.Username, user.Role)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not issue token")
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token, "user": publicUser(user)})
}

func publicUser(u *store.User) gin.H {
	return gin.H{
		"id":           u.ID,
		"username":     u.Username,
		"display_name": u.DisplayName,
		"role":         u.Role,
		"created_at":   u.CreatedAt,
		"updated_at":   u.UpdatedAt,
	}
}

func (s *Server) getMe(c *gin.Context) {
	user, err := store.GetUserByID(c.Request.Context(), s.db, auth.UserIDFromContext(c))
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not load profile")
		return
	}
	c.JSON(http.StatusOK, publicUser(user))
}

type patchMeRequest struct {
	DisplayName *string `json:"display_name"`
	Password    *string `json:"password"`
}

func (s *Server) patchMe(c *gin.Context) {
	var req patchMeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request")
		return
	}
	user, err := store.GetUserByID(c.Request.Context(), s.db, auth.UserIDFromContext(c))
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not load profile")
		return
	}
	display := user.DisplayName
	if req.DisplayName != nil {
		display = strings.TrimSpace(*req.DisplayName)
	}
	hash := ""
	if req.Password != nil && *req.Password != "" {
		h, err := auth.HashPassword(*req.Password)
		if err != nil {
			writeError(c, http.StatusInternalServerError, "could not hash password")
			return
		}
		hash = h
	}
	updated, err := store.UpdateUser(c.Request.Context(), s.db, user.ID, display, hash)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not update profile")
		return
	}
	c.JSON(http.StatusOK, publicUser(updated))
}

type createConversationRequest struct {
	Title string `json:"title"`
}

func (s *Server) createConversation(c *gin.Context) {
	var req createConversationRequest
	_ = c.ShouldBindJSON(&req)
	conv, err := store.CreateConversation(c.Request.Context(), s.db, auth.UserIDFromContext(c), req.Title)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not create conversation")
		return
	}
	c.JSON(http.StatusCreated, conv)
}

func (s *Server) listConversations(c *gin.Context) {
	list, err := store.ListConversationsForUser(c.Request.Context(), s.db, auth.UserIDFromContext(c), c.Query("q"))
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not list conversations")
		return
	}
	c.JSON(http.StatusOK, list)
}

func (s *Server) deleteConversation(c *gin.Context) {
	err := store.DeleteConversationForUser(c.Request.Context(), s.db, c.Param("id"), auth.UserIDFromContext(c))
	if err == sql.ErrNoRows {
		writeError(c, http.StatusNotFound, "conversation not found")
		return
	}
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not delete conversation")
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) getConversation(c *gin.Context) {
	conv, err := store.GetConversation(c.Request.Context(), s.db, c.Param("id"))
	if err == sql.ErrNoRows {
		writeError(c, http.StatusNotFound, "conversation not found")
		return
	}
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not load conversation")
		return
	}
	if conv.UserID != auth.UserIDFromContext(c) {
		writeError(c, http.StatusForbidden, "forbidden")
		return
	}
	msgs, err := store.ListMessages(c.Request.Context(), s.db, conv.ID)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not load messages")
		return
	}
	c.JSON(http.StatusOK, gin.H{"conversation": conv, "messages": msgs})
}

type postMessageRequest struct {
	Body string `json:"body" binding:"required"`
}

func (s *Server) postMessage(c *gin.Context) {
	var req postMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Body) == "" {
		writeError(c, http.StatusBadRequest, "body is required")
		return
	}
	userID := auth.UserIDFromContext(c)
	conv, err := store.GetConversation(c.Request.Context(), s.db, c.Param("id"))
	if err == sql.ErrNoRows {
		writeError(c, http.StatusNotFound, "conversation not found")
		return
	}
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not load conversation")
		return
	}
	if conv.UserID != userID {
		writeError(c, http.StatusForbidden, "forbidden")
		return
	}

	userMsg, err := store.InsertMessage(c.Request.Context(), s.db, conv.ID, "user", strings.TrimSpace(req.Body))
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not save message")
		return
	}
	if conv.Title == "New chat" {
		title := req.Body
		if utf8.RuneCountInString(title) > 48 {
			title = string([]rune(title)[:48]) + "…"
		}
		_ = store.TouchConversationTitle(c.Request.Context(), s.db, conv.ID, title)
	}

	historyRows, err := store.ListMessages(c.Request.Context(), s.db, conv.ID)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not load history")
		return
	}
	history := make([]llm.Message, 0, len(historyRows))
	for _, m := range historyRows {
		if m.ID == userMsg.ID {
			continue
		}
		history = append(history, llm.Message{Role: m.Role, Content: m.Body})
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	c.Writer.Flush()

	writeSSE := func(payload any) error {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		_, err = io.WriteString(c.Writer, "data: "+string(b)+"\n\n")
		if err != nil {
			return err
		}
		c.Writer.Flush()
		return nil
	}

	_ = writeSSE(gin.H{"type": "user", "message": userMsg})

	baseRules, err := store.RulesForUser(c.Request.Context(), s.db, userID)
	if err != nil {
		_ = writeSSE(gin.H{"type": "error", "error": "could not load rules"})
		return
	}
	hits, err := store.SearchKnowledgeChunks(c.Request.Context(), s.db, userMsg.Body, 8)
	if err != nil {
		_ = writeSSE(gin.H{"type": "error", "error": "could not search documentation"})
		return
	}
	system := knowledge.BuildSystemPrompt(baseRules, store.FormatKnowledgeHits(hits))

	s.applyChatSettings(c.Request.Context())
	full, err := s.llm.CompleteStream(c.Request.Context(), system, userMsg.Body, history, func(delta string) error {
		return writeSSE(gin.H{"type": "delta", "text": delta})
	})
	if err != nil {
		_ = writeSSE(gin.H{"type": "error", "error": err.Error()})
		return
	}

	_ = writeSSE(gin.H{"type": "complete", "text": full})

	asst, err := store.InsertMessage(c.Request.Context(), s.db, conv.ID, "assistant", full)
	if err != nil {
		_ = writeSSE(gin.H{"type": "error", "error": "could not save assistant message"})
		return
	}
	_ = writeSSE(gin.H{"type": "done", "message": asst})
}

func (s *Server) adminSummary(c *gin.Context) {
	stats, err := store.Summary(c.Request.Context(), s.db)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not load summary")
		return
	}
	c.JSON(http.StatusOK, stats)
}

func (s *Server) adminChatStats(c *gin.Context) {
	stats, err := store.GetChatStats(c.Request.Context(), s.db)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not load stats")
		return
	}
	c.JSON(http.StatusOK, stats)
}

func (s *Server) adminConversations(c *gin.Context) {
	list, err := store.AdminListConversations(c.Request.Context(), s.db, c.Query("user_id"))
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not list conversations")
		return
	}
	c.JSON(http.StatusOK, list)
}

func (s *Server) adminConversation(c *gin.Context) {
	conv, err := store.GetConversation(c.Request.Context(), s.db, c.Param("id"))
	if err == sql.ErrNoRows {
		writeError(c, http.StatusNotFound, "conversation not found")
		return
	}
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not load conversation")
		return
	}
	msgs, err := store.ListMessages(c.Request.Context(), s.db, conv.ID)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not load messages")
		return
	}
	c.JSON(http.StatusOK, gin.H{"conversation": conv, "messages": msgs})
}

func (s *Server) getGlobalRule(c *gin.Context) {
	body, err := store.GetGlobalRule(c.Request.Context(), s.db)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not load rules")
		return
	}
	c.JSON(http.StatusOK, gin.H{"body": body})
}

type ruleBodyRequest struct {
	Body string `json:"body"`
}

func (s *Server) putGlobalRule(c *gin.Context) {
	var req ruleBodyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request")
		return
	}
	if err := store.PutGlobalRule(c.Request.Context(), s.db, req.Body); err != nil {
		writeError(c, http.StatusInternalServerError, "could not save rules")
		return
	}
	c.JSON(http.StatusOK, gin.H{"body": req.Body})
}

func (s *Server) listGroups(c *gin.Context) {
	list, err := store.ListGroups(c.Request.Context(), s.db)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not list groups")
		return
	}
	c.JSON(http.StatusOK, list)
}

type createGroupRequest struct {
	Name      string   `json:"name" binding:"required"`
	MemberIDs []string `json:"member_ids"`
}

func (s *Server) createGroup(c *gin.Context) {
	var req createGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(c, http.StatusBadRequest, "name is required")
		return
	}
	g, err := store.CreateGroup(c.Request.Context(), s.db, strings.TrimSpace(req.Name), req.MemberIDs)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not create group")
		return
	}
	c.JSON(http.StatusCreated, g)
}

func (s *Server) putGroupRule(c *gin.Context) {
	var req ruleBodyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request")
		return
	}
	if err := store.PutGroupRule(c.Request.Context(), s.db, c.Param("id"), req.Body); err != nil {
		writeError(c, http.StatusInternalServerError, "could not save group rules")
		return
	}
	c.JSON(http.StatusOK, gin.H{"body": req.Body})
}

type membersRequest struct {
	MemberIDs []string `json:"member_ids"`
}

func (s *Server) putGroupMembers(c *gin.Context) {
	var req membersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request")
		return
	}
	if err := store.PutGroupMembers(c.Request.Context(), s.db, c.Param("id"), req.MemberIDs); err != nil {
		writeError(c, http.StatusInternalServerError, "could not save members")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

type effectiveChat struct {
	Base      string
	Key       string
	Model     string
	KeySource string
}

func (s *Server) effectiveChat(ctx context.Context) effectiveChat {
	out := effectiveChat{
		Base:  config.ResolveChatBaseURL(s.cfg.ChatBaseURL),
		Key:   strings.TrimSpace(s.cfg.ChatAPIKey),
		Model: strings.TrimSpace(s.cfg.ChatModel),
	}
	if out.Model == "" {
		out.Model = "auto"
	}
	if out.Key != "" {
		out.KeySource = "env"
	}
	saved, err := store.GetChatSettings(ctx, s.db)
	if err != nil || saved == nil {
		return out
	}
	if strings.TrimSpace(saved.BaseURL) != "" {
		out.Base = config.ResolveChatBaseURL(saved.BaseURL)
	}
	if strings.TrimSpace(saved.Model) != "" {
		out.Model = strings.TrimSpace(saved.Model)
	}
	if strings.TrimSpace(saved.APIKey) != "" {
		out.Key = strings.TrimSpace(saved.APIKey)
		out.KeySource = "settings"
	}
	return out
}

func (s *Server) applyChatSettings(ctx context.Context) {
	if s.llm == nil {
		return
	}
	eff := s.effectiveChat(ctx)
	s.llm.BaseURL = eff.Base
	s.llm.APIKey = eff.Key
	s.llm.Model = eff.Model
}

func (s *Server) indexGuideSources(ctx context.Context) (int, error) {
	folders := []string{}
	saved, err := store.GetChatSettings(ctx, s.db)
	if err == nil && saved != nil {
		folders = saved.AllowedFolders
	}
	return knowledge.IndexGuidePaths(ctx, s.db, folders, s.cfg.KnowledgeDir)
}

func (s *Server) settingsPayload(ctx context.Context) gin.H {
	s.applyChatSettings(ctx)
	eff := s.effectiveChat(ctx)
	folders := []string{}
	saved, err := store.GetChatSettings(ctx, s.db)
	if err == nil && saved != nil {
		folders = saved.AllowedFolders
	}
	if folders == nil {
		folders = []string{}
	}
	return gin.H{
		"chat_base_url":       eff.Base,
		"chat_model":          eff.Model,
		"chat_api_key_set":    strings.TrimSpace(eff.Key) != "",
		"chat_api_key_hint":   store.APIKeyHint(eff.Key),
		"chat_api_key_source": eff.KeySource,
		"allowed_folders":     folders,
	}
}

func (s *Server) chatReady(c *gin.Context) {
	eff := s.effectiveChat(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{
		"ready": strings.TrimSpace(eff.Key) != "" && strings.TrimSpace(eff.Base) != "",
	})
}

func (s *Server) getSettings(c *gin.Context) {
	c.JSON(http.StatusOK, s.settingsPayload(c.Request.Context()))
}

type putSettingsRequest struct {
	ChatBaseURL     string    `json:"chat_base_url"`
	ChatModel       string    `json:"chat_model"`
	ChatAPIKey      *string   `json:"chat_api_key"`
	ClearChatAPIKey bool      `json:"clear_chat_api_key"`
	AllowedFolders  *[]string `json:"allowed_folders"`
}

func (s *Server) putSettings(c *gin.Context) {
	var req putSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request")
		return
	}
	base := strings.TrimSpace(req.ChatBaseURL)
	model := strings.TrimSpace(req.ChatModel)
	if model == "" {
		model = "auto"
	}
	updateKey := req.ClearChatAPIKey || req.ChatAPIKey != nil
	key := ""
	if req.ClearChatAPIKey {
		key = ""
	} else if req.ChatAPIKey != nil {
		key = strings.TrimSpace(*req.ChatAPIKey)
	}
	folders := []string{}
	saved, err := store.GetChatSettings(c.Request.Context(), s.db)
	if err == nil && saved != nil {
		folders = saved.AllowedFolders
	}
	if req.AllowedFolders != nil {
		folders = knowledge.NormalizeGuidePaths(*req.AllowedFolders)
	}
	if err := store.PutChatSettings(c.Request.Context(), s.db, base, model, key, updateKey, folders); err != nil {
		writeError(c, http.StatusInternalServerError, "could not save settings")
		return
	}
	indexCtx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	if _, err := s.indexGuideSources(indexCtx); err != nil {
		writeError(c, http.StatusInternalServerError, "could not index documentation")
		return
	}
	s.getSettings(c)
}

func (s *Server) listUsers(c *gin.Context) {
	list, err := store.ListUsers(c.Request.Context(), s.db)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not list users")
		return
	}
	out := make([]gin.H, 0, len(list))
	for i := range list {
		out = append(out, publicUser(&list[i]))
	}
	c.JSON(http.StatusOK, out)
}

type createUserRequest struct {
	Username    string `json:"username" binding:"required"`
	Password    string `json:"password" binding:"required"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

func (s *Server) createUser(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request")
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || strings.TrimSpace(req.Password) == "" {
		writeError(c, http.StatusBadRequest, "username and password are required")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not hash password")
		return
	}
	user, err := store.CreateUser(c.Request.Context(), s.db, username, hash, req.DisplayName, req.Role)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(c, http.StatusConflict, "username already exists")
			return
		}
		writeError(c, http.StatusInternalServerError, "could not create user")
		return
	}
	c.JSON(http.StatusCreated, publicUser(user))
}

type patchUserRequest struct {
	Username    *string `json:"username"`
	DisplayName *string `json:"display_name"`
	Role        *string `json:"role"`
	Password    *string `json:"password"`
}

func (s *Server) patchUser(c *gin.Context) {
	var req patchUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request")
		return
	}
	current, err := store.GetUserByID(c.Request.Context(), s.db, c.Param("id"))
	if err == sql.ErrNoRows {
		writeError(c, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not load user")
		return
	}
	username := current.Username
	display := current.DisplayName
	role := current.Role
	if req.Username != nil {
		username = strings.TrimSpace(*req.Username)
	}
	if req.DisplayName != nil {
		display = strings.TrimSpace(*req.DisplayName)
	}
	if req.Role != nil {
		role = strings.TrimSpace(*req.Role)
	}
	hash := ""
	if req.Password != nil && *req.Password != "" {
		h, err := auth.HashPassword(*req.Password)
		if err != nil {
			writeError(c, http.StatusInternalServerError, "could not hash password")
			return
		}
		hash = h
	}
	updated, err := store.AdminUpdateUser(c.Request.Context(), s.db, current.ID, username, display, role, hash)
	if err == store.ErrLastAdmin {
		writeError(c, http.StatusConflict, "cannot remove the last admin")
		return
	}
	if err == sql.ErrNoRows {
		writeError(c, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeError(c, http.StatusConflict, "username already exists")
			return
		}
		writeError(c, http.StatusInternalServerError, "could not update user")
		return
	}
	c.JSON(http.StatusOK, publicUser(updated))
}

func (s *Server) deleteUser(c *gin.Context) {
	err := store.DeleteUser(c.Request.Context(), s.db, c.Param("id"))
	if err == store.ErrLastAdmin {
		writeError(c, http.StatusConflict, "cannot delete the last admin")
		return
	}
	if err == sql.ErrNoRows {
		writeError(c, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not delete user")
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) reindexKnowledge(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	n, err := s.indexGuideSources(ctx)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "could not index documentation")
		return
	}
	c.JSON(http.StatusOK, gin.H{"chunks": n})
}
