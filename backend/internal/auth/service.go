package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var ErrUnauthorized = errors.New("unauthorized")

type Organization struct {
	ID   string `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type Principal struct {
	UserID       string        `json:"id"`
	Username     string        `json:"username"`
	DisplayName  string        `json:"display_name"`
	Email        string        `json:"email,omitempty"`
	Organization *Organization `json:"organization,omitempty"`
	Roles        []string      `json:"roles"`
	SessionID    string        `json:"-"`
	TokenHash    string        `json:"-"`
}

func (p Principal) HasRole(role string) bool {
	for _, current := range p.Roles {
		if current == role {
			return true
		}
	}
	return false
}

type LoginInput struct {
	Username  string
	Password  string
	IPAddress string
	UserAgent string
	RequestID string
}

type LoginResult struct {
	Token     string
	ExpiresAt time.Time
	User      Principal
}

type Service struct {
	pool *pgxpool.Pool
	ttl  time.Duration
}

func NewService(pool *pgxpool.Pool, ttl time.Duration) *Service {
	return &Service{pool: pool, ttl: ttl}
}

func (s *Service) Login(ctx context.Context, input LoginInput) (LoginResult, error) {
	username := strings.TrimSpace(input.Username)
	if username == "" || input.Password == "" {
		return LoginResult{}, ErrUnauthorized
	}

	var user Principal
	var passwordHash string
	var organizationID, organizationCode, organizationName, organizationType *string
	err := s.pool.QueryRow(ctx, `
		SELECT u.id::text, u.username, u.password_hash, u.display_name, COALESCE(u.email, ''),
		       o.id::text, o.organization_code, o.organization_name, o.organization_type
		FROM users u
		LEFT JOIN LATERAL (
			SELECT org.id, org.organization_code, org.organization_name, org.organization_type
			FROM user_organizations uo
			JOIN organizations org ON org.id = uo.organization_id
			WHERE uo.user_id = u.id
			ORDER BY uo.is_primary DESC, uo.created_at ASC
			LIMIT 1
		) o ON true
		WHERE lower(u.username) = lower($1)
		  AND u.deleted_at IS NULL
		  AND u.status = 'enabled'
	`, username).Scan(
		&user.UserID, &user.Username, &passwordHash, &user.DisplayName, &user.Email,
		&organizationID, &organizationCode, &organizationName, &organizationType,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		// Keep the nonexistent-user path computationally similar to a failed password.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$12$dlTGU4v9xUc4hcROBbvaMeh2hJ4GLkVDJw1E1rn9vYdGkO/kx6TwS"), []byte(input.Password))
		return LoginResult{}, ErrUnauthorized
	}
	if err != nil {
		return LoginResult{}, fmt.Errorf("load user: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(input.Password)); err != nil {
		_ = s.writeLoginFailure(ctx, user.UserID, input)
		return LoginResult{}, ErrUnauthorized
	}

	roles, err := s.loadRoles(ctx, user.UserID)
	if err != nil {
		return LoginResult{}, err
	}
	user.Roles = roles
	if organizationID != nil {
		user.Organization = &Organization{
			ID:   *organizationID,
			Code: dereference(organizationCode),
			Name: dereference(organizationName),
			Type: dereference(organizationType),
		}
	}

	token, tokenHash, err := newToken()
	if err != nil {
		return LoginResult{}, fmt.Errorf("generate session token: %w", err)
	}
	expiresAt := time.Now().UTC().Add(s.ttl)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LoginResult{}, fmt.Errorf("begin login transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var sessionID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO sessions (user_id, token_hash, expires_at, ip_address, user_agent)
		VALUES ($1::uuid, $2, $3, NULLIF($4, '')::inet, NULLIF($5, ''))
		RETURNING id::text
	`, user.UserID, tokenHash, expiresAt, input.IPAddress, truncate(input.UserAgent, 2000)).Scan(&sessionID); err != nil {
		return LoginResult{}, fmt.Errorf("create session: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET last_login_at = now(), updated_at = now() WHERE id = $1::uuid`, user.UserID); err != nil {
		return LoginResult{}, fmt.Errorf("update last login: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, request_ip, request_id)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, 'auth.login', 'user', $1, '用户登录系统', NULLIF($3, '')::inet, NULLIF($4, ''))
	`, user.UserID, organizationIDValue(user.Organization), input.IPAddress, input.RequestID); err != nil {
		return LoginResult{}, fmt.Errorf("write login audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return LoginResult{}, fmt.Errorf("commit login transaction: %w", err)
	}

	user.SessionID = sessionID
	user.TokenHash = tokenHash
	return LoginResult{Token: token, ExpiresAt: expiresAt, User: user}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	if token == "" {
		return Principal{}, ErrUnauthorized
	}
	tokenHash := hashToken(token)

	var user Principal
	var organizationID, organizationCode, organizationName, organizationType *string
	err := s.pool.QueryRow(ctx, `
		SELECT u.id::text, u.username, u.display_name, COALESCE(u.email, ''),
		       s.id::text, o.id::text, o.organization_code, o.organization_name, o.organization_type
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		LEFT JOIN LATERAL (
			SELECT org.id, org.organization_code, org.organization_name, org.organization_type
			FROM user_organizations uo
			JOIN organizations org ON org.id = uo.organization_id
			WHERE uo.user_id = u.id
			ORDER BY uo.is_primary DESC, uo.created_at ASC
			LIMIT 1
		) o ON true
		WHERE s.token_hash = $1
		  AND s.revoked_at IS NULL
		  AND s.expires_at > now()
		  AND u.deleted_at IS NULL
		  AND u.status = 'enabled'
	`, tokenHash).Scan(
		&user.UserID, &user.Username, &user.DisplayName, &user.Email, &user.SessionID,
		&organizationID, &organizationCode, &organizationName, &organizationType,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrUnauthorized
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate session: %w", err)
	}
	user.TokenHash = tokenHash
	if organizationID != nil {
		user.Organization = &Organization{ID: *organizationID, Code: dereference(organizationCode), Name: dereference(organizationName), Type: dereference(organizationType)}
	}
	roles, err := s.loadRoles(ctx, user.UserID)
	if err != nil {
		return Principal{}, err
	}
	user.Roles = roles

	_, _ = s.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE id = $1::uuid AND last_seen_at < now() - interval '5 minutes'`, user.SessionID)
	return user, nil
}

func (s *Service) Logout(ctx context.Context, principal Principal, requestID, ipAddress string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin logout transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE id = $1::uuid AND revoked_at IS NULL`, principal.SessionID); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, request_ip, request_id)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, 'auth.logout', 'user', $1, '用户退出系统', NULLIF($3, '')::inet, NULLIF($4, ''))
	`, principal.UserID, organizationIDValue(principal.Organization), ipAddress, requestID); err != nil {
		return fmt.Errorf("write logout audit: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *Service) loadRoles(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT r.role_key
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = $1::uuid
		ORDER BY r.role_key
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("load roles: %w", err)
	}
	defer rows.Close()
	roles := make([]string, 0)
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, fmt.Errorf("scan role: %w", err)
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate roles: %w", err)
	}
	return roles, nil
}

func (s *Service) writeLoginFailure(ctx context.Context, userID string, input LoginInput) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_logs (user_id, action, resource_type, resource_id, summary, request_ip, request_id, result)
		VALUES ($1::uuid, 'auth.login', 'user', $1, '登录失败：密码错误', NULLIF($2, '')::inet, NULLIF($3, ''), 'failure')
	`, userID, input.IPAddress, input.RequestID)
	return err
}

func newToken() (string, string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buffer)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func organizationIDValue(organization *Organization) string {
	if organization == nil {
		return ""
	}
	return organization.ID
}

func dereference(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
