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

	"netassets/internal/pagination"
)

const (
	AuthenticationSession  = "session"
	AuthenticationAPIToken = "api_token"
)

var (
	ErrUnauthorized          = errors.New("unauthorized")
	ErrOrganizationForbidden = errors.New("organization forbidden")
	ErrInvalid               = errors.New("invalid input")
	ErrSessionNotFound       = errors.New("session not found")
	ErrCurrentSession        = errors.New("current session cannot be revoked")
)

type Organization struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	IsPrimary bool   `json:"is_primary,omitempty"`
}

type Principal struct {
	RepairEnabled      bool           `json:"repair_enabled"`
	ExecutionOnly      bool           `json:"execution_only"`
	UserID             string         `json:"id"`
	Username           string         `json:"username"`
	DisplayName        string         `json:"display_name"`
	Email              string         `json:"email,omitempty"`
	MobilePhone        string         `json:"mobile_phone,omitempty"`
	OfficePhone        string         `json:"office_phone,omitempty"`
	MustChangePassword bool           `json:"must_change_password"`
	Organization       *Organization  `json:"organization,omitempty"`
	Organizations      []Organization `json:"organizations"`
	Roles              []string       `json:"roles"`
	Permissions        []string       `json:"permissions"`
	SessionID          string         `json:"-"`
	TokenHash          string         `json:"-"`
	APITokenID         string         `json:"-"`
	AuthenticationType string         `json:"-"`
}

func (p Principal) HasRole(role string) bool {
	for _, current := range p.Roles {
		if current == role {
			return true
		}
	}
	return false
}

func (p Principal) HasPermission(permission string) bool {
	for _, current := range p.Permissions {
		if current == permission {
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

type ProfileInput struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	MobilePhone string `json:"mobile_phone"`
	OfficePhone string `json:"office_phone"`
}

type ChangePasswordInput struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// Session describes one active login belonging to the signed-in user.
// The raw user agent is retained for troubleshooting while the derived labels
// keep the account settings page understandable for non-technical users.
type Session struct {
	ID              string    `json:"id"`
	IPAddress       string    `json:"ip_address,omitempty"`
	UserAgent       string    `json:"user_agent,omitempty"`
	Device          string    `json:"device"`
	Browser         string    `json:"browser"`
	OperatingSystem string    `json:"operating_system"`
	CreatedAt       time.Time `json:"created_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	Current         bool      `json:"current"`
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
	var primaryOrganizationID *string
	err := s.pool.QueryRow(ctx, `
		SELECT u.id::text, u.username, u.password_hash, u.display_name, COALESCE(u.email, ''), COALESCE(u.mobile_phone, ''), COALESCE(u.office_phone, ''), u.must_change_password,
		       (SELECT uo.organization_id::text FROM user_organizations uo
		        WHERE uo.user_id = u.id ORDER BY uo.is_primary DESC, uo.created_at ASC LIMIT 1)
		FROM users u
		WHERE lower(u.username) = lower($1)
		  AND u.deleted_at IS NULL
		  AND u.status = 'enabled'
	`, username).Scan(&user.UserID, &user.Username, &passwordHash, &user.DisplayName, &user.Email, &user.MobilePhone, &user.OfficePhone, &user.MustChangePassword, &primaryOrganizationID)
	if errors.Is(err, pgx.ErrNoRows) {
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
	if err := s.loadPrincipalDetails(ctx, &user, dereference(primaryOrganizationID)); err != nil {
		return LoginResult{}, err
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
		INSERT INTO sessions (user_id, token_hash, expires_at, current_organization_id, ip_address, user_agent)
		VALUES ($1::uuid, $2, $3, NULLIF($4, '')::uuid, NULLIF($5, '')::inet, NULLIF($6, ''))
		RETURNING id::text
	`, user.UserID, tokenHash, expiresAt, organizationIDValue(user.Organization), input.IPAddress, truncate(input.UserAgent, 2000)).Scan(&sessionID); err != nil {
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
	user.AuthenticationType = AuthenticationSession
	return LoginResult{Token: token, ExpiresAt: expiresAt, User: user}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	if token == "" {
		return Principal{}, ErrUnauthorized
	}
	tokenHash := hashToken(token)
	var user Principal
	var activeOrganizationID *string
	err := s.pool.QueryRow(ctx, `
		SELECT u.id::text, u.username, u.display_name, COALESCE(u.email, ''), COALESCE(u.mobile_phone, ''), COALESCE(u.office_phone, ''), u.must_change_password, s.id::text,
		       s.current_organization_id::text
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1
		  AND s.revoked_at IS NULL
		  AND s.expires_at > now()
		  AND u.deleted_at IS NULL
		  AND u.status = 'enabled'
	`, tokenHash).Scan(&user.UserID, &user.Username, &user.DisplayName, &user.Email, &user.MobilePhone, &user.OfficePhone, &user.MustChangePassword, &user.SessionID, &activeOrganizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrUnauthorized
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate session: %w", err)
	}
	user.TokenHash = tokenHash
	user.AuthenticationType = AuthenticationSession
	if err := s.loadPrincipalDetails(ctx, &user, dereference(activeOrganizationID)); err != nil {
		return Principal{}, err
	}
	_, _ = s.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE id = $1::uuid AND last_seen_at < now() - interval '5 minutes'`, user.SessionID)
	return user, nil
}

// UpdateProfile allows a signed-in user to maintain only their own basic contact
// information. Authorization assignments deliberately remain in the user-management domain.
func (s *Service) UpdateProfile(ctx context.Context, principal Principal, input ProfileInput, requestID, ipAddress string) (Principal, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.TrimSpace(input.Email)
	input.MobilePhone = strings.TrimSpace(input.MobilePhone)
	input.OfficePhone = strings.TrimSpace(input.OfficePhone)
	if input.DisplayName == "" || len(input.DisplayName) > 128 || len(input.Email) > 255 || len(input.MobilePhone) > 64 || len(input.OfficePhone) > 64 {
		return Principal{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Principal{}, fmt.Errorf("begin update profile: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE users SET display_name=$2, email=NULLIF($3,''), mobile_phone=NULLIF($4,''), office_phone=NULLIF($5,''), updated_at=now() WHERE id=$1::uuid AND deleted_at IS NULL`, principal.UserID, input.DisplayName, input.Email, input.MobilePhone, input.OfficePhone)
	if err != nil {
		return Principal{}, fmt.Errorf("update profile: %w", err)
	}
	if command.RowsAffected() == 0 {
		return Principal{}, ErrUnauthorized
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, request_ip, request_id)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, 'auth.profile_update', 'user', $1, '用户更新个人资料', NULLIF($3, '')::inet, NULLIF($4, ''))
	`, principal.UserID, organizationIDValue(principal.Organization), ipAddress, requestID); err != nil {
		return Principal{}, fmt.Errorf("audit profile update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Principal{}, fmt.Errorf("commit profile update: %w", err)
	}
	return s.CurrentBySession(ctx, principal.SessionID)
}

// ChangePassword verifies the current password and keeps only the current session
// alive. This protects other devices while not interrupting the user who changed it.
func (s *Service) ChangePassword(ctx context.Context, principal Principal, input ChangePasswordInput, requestID, ipAddress string) error {
	if input.CurrentPassword == "" || len(input.NewPassword) < 10 {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin change password: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var passwordHash string
	if err := tx.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1::uuid AND deleted_at IS NULL AND status='enabled' FOR UPDATE`, principal.UserID).Scan(&passwordHash); errors.Is(err, pgx.ErrNoRows) {
		return ErrUnauthorized
	} else if err != nil {
		return fmt.Errorf("load password hash: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(input.CurrentPassword)); err != nil {
		return ErrUnauthorized
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), 12)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2, password_changed_at=now(), must_change_password=false, updated_at=now() WHERE id=$1::uuid`, principal.UserID, string(hash)); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE user_id=$1::uuid AND id<>$2::uuid AND revoked_at IS NULL`, principal.UserID, principal.SessionID); err != nil {
		return fmt.Errorf("revoke other sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, request_ip, request_id)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, 'auth.password_change', 'user', $1, '用户修改登录密码', NULLIF($3, '')::inet, NULLIF($4, ''))
	`, principal.UserID, organizationIDValue(principal.Organization), ipAddress, requestID); err != nil {
		return fmt.Errorf("audit password change: %w", err)
	}
	return tx.Commit(ctx)
}

// SwitchOrganization changes only the session's current work context. It never changes
// the user's primary appointment. The requested organization must be an enabled current appointment.
func (s *Service) SwitchOrganization(ctx context.Context, principal Principal, organizationID, requestID, ipAddress string) (Principal, error) {
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return Principal{}, ErrOrganizationForbidden
	}
	var allowed bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM user_organizations uo
			JOIN organizations o ON o.id = uo.organization_id
			WHERE uo.user_id = $1::uuid AND uo.organization_id = $2::uuid AND o.status = 'enabled' AND o.deleted_at IS NULL
		)
	`, principal.UserID, organizationID).Scan(&allowed); err != nil {
		return Principal{}, fmt.Errorf("verify working organization: %w", err)
	}
	if !allowed {
		return Principal{}, ErrOrganizationForbidden
	}
	if _, err := s.pool.Exec(ctx, `UPDATE sessions SET current_organization_id = $1::uuid WHERE id = $2::uuid AND revoked_at IS NULL`, organizationID, principal.SessionID); err != nil {
		return Principal{}, fmt.Errorf("switch working organization: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, 'auth.switch_organization', 'organization', $2, '切换当前工作组织', NULLIF($3, '')::inet, NULLIF($4, ''))
	`, principal.UserID, organizationID, ipAddress, requestID); err != nil {
		return Principal{}, fmt.Errorf("write switch organization audit: %w", err)
	}
	return s.CurrentBySession(ctx, principal.SessionID)
}

func (s *Service) CurrentBySession(ctx context.Context, sessionID string) (Principal, error) {
	var user Principal
	var activeOrganizationID *string
	err := s.pool.QueryRow(ctx, `
		SELECT u.id::text, u.username, u.display_name, COALESCE(u.email, ''), COALESCE(u.mobile_phone, ''), COALESCE(u.office_phone, ''), u.must_change_password, s.id::text, s.current_organization_id::text
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id = $1::uuid AND s.revoked_at IS NULL AND s.expires_at > now()
		  AND u.deleted_at IS NULL AND u.status = 'enabled'
	`, sessionID).Scan(&user.UserID, &user.Username, &user.DisplayName, &user.Email, &user.MobilePhone, &user.OfficePhone, &user.MustChangePassword, &user.SessionID, &activeOrganizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrUnauthorized
	}
	if err != nil {
		return Principal{}, fmt.Errorf("load current session: %w", err)
	}
	user.AuthenticationType = AuthenticationSession
	if err := s.loadPrincipalDetails(ctx, &user, dereference(activeOrganizationID)); err != nil {
		return Principal{}, err
	}
	return user, nil
}

// ListSessions is retained for internal compatibility. New HTTP callers should
// use SearchSessions so active-device lists always use the common page contract.
func (s *Service) ListSessions(ctx context.Context, principal Principal) ([]Session, error) {
	result, err := s.SearchSessions(ctx, principal, pagination.Query{Page: 1, PageSize: pagination.MaxPageSize})
	if err != nil {
		return nil, err
	}
	return result.Items, nil
}

// SearchSessions returns only currently usable sessions owned by the signed-in
// user. The current session is ordered first so it is always visible on page 1.
func (s *Service) SearchSessions(ctx context.Context, principal Principal, page pagination.Query) (pagination.Result[Session], error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, COALESCE(host(ip_address), ''), COALESCE(user_agent, ''), created_at, last_seen_at, expires_at,
		       count(*) OVER ()
		FROM sessions
		WHERE user_id=$1::uuid AND revoked_at IS NULL AND expires_at > now()
		ORDER BY (id=$2::uuid) DESC, last_seen_at DESC, created_at DESC
		LIMIT $3 OFFSET $4
	`, principal.UserID, principal.SessionID, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[Session]{}, fmt.Errorf("list active sessions: %w", err)
	}
	defer rows.Close()
	items := make([]Session, 0)
	total := 0
	for rows.Next() {
		var item Session
		if err := rows.Scan(&item.ID, &item.IPAddress, &item.UserAgent, &item.CreatedAt, &item.LastSeenAt, &item.ExpiresAt, &total); err != nil {
			return pagination.Result[Session]{}, fmt.Errorf("scan active session: %w", err)
		}
		item.Current = item.ID == principal.SessionID
		item.Device, item.Browser, item.OperatingSystem = describeUserAgent(item.UserAgent)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Session]{}, fmt.Errorf("iterate active sessions: %w", err)
	}
	// count(*) OVER() is unavailable when an out-of-range page produces no rows.
	// Return the actual total so the profile UI can safely step back one page.
	if pagination.NeedsTotalFallback(len(items), page) {
		if err := s.pool.QueryRow(ctx, `
			SELECT count(*)
			FROM sessions
			WHERE user_id=$1::uuid AND revoked_at IS NULL AND expires_at > now()
		`, principal.UserID).Scan(&total); err != nil {
			return pagination.Result[Session]{}, fmt.Errorf("count active sessions: %w", err)
		}
	}
	return pagination.NewResult(items, total, page), nil
}

// RevokeSession signs the user out from one other active device. A caller cannot
// accidentally invalidate the request's own session; normal logout handles that case.
func (s *Service) RevokeSession(ctx context.Context, principal Principal, sessionID, requestID, ipAddress string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ErrSessionNotFound
	}
	if sessionID == principal.SessionID {
		return ErrCurrentSession
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin revoke session: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
		UPDATE sessions SET revoked_at=now()
		WHERE id::text=$2 AND user_id=$1::uuid AND revoked_at IS NULL AND expires_at > now()
	`, principal.UserID, sessionID)
	if err != nil {
		return fmt.Errorf("revoke selected session: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrSessionNotFound
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, request_ip, request_id)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, 'auth.session_revoke', 'session', $3, '用户退出一个其他登录设备', NULLIF($4, '')::inet, NULLIF($5, ''))
	`, principal.UserID, organizationIDValue(principal.Organization), sessionID, ipAddress, requestID); err != nil {
		return fmt.Errorf("audit selected session revoke: %w", err)
	}
	return tx.Commit(ctx)
}

// RevokeOtherSessions signs the user out everywhere except the current request session.
func (s *Service) RevokeOtherSessions(ctx context.Context, principal Principal, requestID, ipAddress string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin revoke other sessions: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
		UPDATE sessions SET revoked_at=now()
		WHERE user_id=$1::uuid AND id<>$2::uuid AND revoked_at IS NULL AND expires_at > now()
	`, principal.UserID, principal.SessionID)
	if err != nil {
		return 0, fmt.Errorf("revoke other sessions: %w", err)
	}
	count := command.RowsAffected()
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, 'auth.sessions_revoke_others', 'user', $1, '用户退出其他全部登录设备', jsonb_build_object('revoked_count', $3::bigint), NULLIF($4, '')::inet, NULLIF($5, ''))
	`, principal.UserID, organizationIDValue(principal.Organization), count, ipAddress, requestID); err != nil {
		return 0, fmt.Errorf("audit other sessions revoke: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit other sessions revoke: %w", err)
	}
	return count, nil
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

func (s *Service) loadPrincipalDetails(ctx context.Context, user *Principal, activeOrganizationID string) error {
	organizations, err := s.loadOrganizations(ctx, user.UserID)
	if err != nil {
		return err
	}
	user.Organizations = organizations
	for index := range organizations {
		if organizations[index].ID == activeOrganizationID {
			copy := organizations[index]
			user.Organization = &copy
			break
		}
	}
	if user.Organization == nil {
		for index := range organizations {
			if organizations[index].IsPrimary {
				copy := organizations[index]
				user.Organization = &copy
				break
			}
		}
	}
	roles, err := s.loadRoles(ctx, user.UserID)
	if err != nil {
		return err
	}
	permissions, err := s.loadPermissions(ctx, user.UserID)
	if err != nil {
		return err
	}
	user.Roles, user.Permissions = roles, permissions
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM remediation_grants WHERE user_id=$1::uuid)`, user.UserID).Scan(&user.RepairEnabled); err != nil {
		return err
	}
	user.ExecutionOnly = user.RepairEnabled && len(roles) == 0
	return nil
}

func (s *Service) loadOrganizations(ctx context.Context, userID string) ([]Organization, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id::text, o.organization_code, o.organization_name, o.organization_type, uo.is_primary
		FROM user_organizations uo
		JOIN organizations o ON o.id = uo.organization_id
		WHERE uo.user_id = $1::uuid AND o.status = 'enabled' AND o.deleted_at IS NULL
		ORDER BY uo.is_primary DESC, o.organization_code
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("load user organizations: %w", err)
	}
	defer rows.Close()
	items := make([]Organization, 0)
	for rows.Next() {
		var item Organization
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.Type, &item.IsPrimary); err != nil {
			return nil, fmt.Errorf("scan user organization: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user organizations: %w", err)
	}
	return items, nil
}

func (s *Service) loadRoles(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT r.role_key
		FROM user_roles ur JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = $1::uuid AND r.status = 'enabled'
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

func (s *Service) loadPermissions(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT rp.permission_key
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
		JOIN role_permissions rp ON rp.role_id = r.id
		WHERE ur.user_id = $1::uuid
		ORDER BY rp.permission_key
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("load permissions: %w", err)
	}
	defer rows.Close()
	items := make([]string, 0)
	for rows.Next() {
		var item string
		if err := rows.Scan(&item); err != nil {
			return nil, fmt.Errorf("scan permission: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate permissions: %w", err)
	}
	return items, nil
}

func (s *Service) writeLoginFailure(ctx context.Context, userID string, input LoginInput) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_logs (user_id, action, resource_type, resource_id, summary, request_ip, request_id, result)
		VALUES ($1::uuid, 'auth.login', 'user', $1, '登录失败：密码错误', NULLIF($2, '')::inet, NULLIF($3, ''), 'failure')
	`, userID, input.IPAddress, input.RequestID)
	return err
}

func describeUserAgent(userAgent string) (device, browser, operatingSystem string) {
	value := strings.ToLower(userAgent)
	device = "电脑"
	switch {
	case strings.Contains(value, "ipad") || strings.Contains(value, "tablet"):
		device = "平板"
	case strings.Contains(value, "iphone") || strings.Contains(value, "android") && strings.Contains(value, "mobile"):
		device = "手机"
	case strings.TrimSpace(value) == "":
		device = "未知设备"
	}

	browser = "其他浏览器"
	switch {
	case strings.Contains(value, "edg/"):
		browser = "Microsoft Edge"
	case strings.Contains(value, "opr/") || strings.Contains(value, "opera"):
		browser = "Opera"
	case strings.Contains(value, "firefox/"):
		browser = "Firefox"
	case strings.Contains(value, "chrome/") || strings.Contains(value, "crios/"):
		browser = "Chrome"
	case strings.Contains(value, "safari/"):
		browser = "Safari"
	case strings.TrimSpace(value) == "":
		browser = "未知浏览器"
	}

	operatingSystem = "其他系统"
	switch {
	case strings.Contains(value, "windows"):
		operatingSystem = "Windows"
	case strings.Contains(value, "iphone") || strings.Contains(value, "ipad") || strings.Contains(value, "cpu os"):
		operatingSystem = "iOS / iPadOS"
	case strings.Contains(value, "android"):
		operatingSystem = "Android"
	case strings.Contains(value, "mac os x") || strings.Contains(value, "macintosh"):
		operatingSystem = "macOS"
	case strings.Contains(value, "linux"):
		operatingSystem = "Linux"
	case strings.TrimSpace(value) == "":
		operatingSystem = "未知系统"
	}
	return device, browser, operatingSystem
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

// RecordRepairBoundaryDenial audits execution accounts attempting management APIs.
func (s *Service) RecordRepairBoundaryDenial(ctx context.Context, p Principal, method, route, requestID, ip string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO audit_logs(user_id,action,resource_type,summary,details,request_id,request_ip,result) VALUES($1::uuid,'remediation.access_denied','remediation','执行账号访问管理接口被拒绝',jsonb_build_object('method',$2::text,'route',$3::text),$4,NULLIF($5,'')::inet,'failure')`, p.UserID, method, route, requestID, ip)
	return err
}
