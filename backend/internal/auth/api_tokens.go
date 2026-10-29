package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"netassets/internal/pagination"
)

const (
	apiTokenPrefix        = "nat_"
	maxActiveAPITokens    = 10
	maxAPITokenExpiryDays = 365
)

var (
	apiTokenUUIDPattern     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
	ErrAPITokenNotFound     = errors.New("api token not found")
	ErrAPITokenLimit        = errors.New("api token limit reached")
	ErrAPITokenNameConflict = errors.New("api token name conflict")
	ErrSessionRequired      = errors.New("browser session required")
)

// APIToken contains only token metadata. The secret value is never persisted
// and is returned separately exactly once when a token is created.
type APIToken struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	TokenPrefix      string     `json:"token_prefix"`
	OrganizationID   string     `json:"organization_id"`
	OrganizationName string     `json:"organization_name"`
	ExpiresAt        time.Time  `json:"expires_at"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
}

type CreateAPITokenInput struct {
	Name          string `json:"name"`
	ExpiresInDays int    `json:"expires_in_days"`
}

type CreateAPITokenResult struct {
	Item  APIToken `json:"item"`
	Token string   `json:"token"`
}

func NormalizeAPITokenInput(input CreateAPITokenInput) (CreateAPITokenInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return CreateAPITokenInput{}, &FieldError{Message: "API Token 名称不能为空"}
	}
	if utf8.RuneCountInString(input.Name) > 100 {
		return CreateAPITokenInput{}, &FieldError{Message: "API Token 名称最多 100 字"}
	}
	if input.ExpiresInDays < 1 || input.ExpiresInDays > maxAPITokenExpiryDays {
		return CreateAPITokenInput{}, &FieldError{Message: "API Token 有效期应为 1 至 365 天"}
	}
	return input, nil
}

func (s *Service) CreateAPIToken(ctx context.Context, principal Principal, input CreateAPITokenInput, requestID, ipAddress string) (CreateAPITokenResult, error) {
	if principal.UserID == "" || principal.SessionID == "" || principal.AuthenticationType != AuthenticationSession || principal.Organization == nil {
		return CreateAPITokenResult{}, ErrSessionRequired
	}
	input, err := NormalizeAPITokenInput(input)
	if err != nil {
		return CreateAPITokenResult{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreateAPITokenResult{}, fmt.Errorf("begin api token creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialize token creation per user so concurrent requests cannot exceed the
	// active-token limit.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "api-token:"+principal.UserID); err != nil {
		return CreateAPITokenResult{}, fmt.Errorf("lock api token creation: %w", err)
	}
	var activeCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM api_tokens WHERE user_id=$1::uuid AND revoked_at IS NULL AND expires_at > now()`, principal.UserID).Scan(&activeCount); err != nil {
		return CreateAPITokenResult{}, fmt.Errorf("count active api tokens: %w", err)
	}
	if activeCount >= maxActiveAPITokens {
		return CreateAPITokenResult{}, ErrAPITokenLimit
	}

	raw, _, err := newToken()
	if err != nil {
		return CreateAPITokenResult{}, fmt.Errorf("generate api token: %w", err)
	}
	secret := apiTokenPrefix + raw
	tokenHash := hashToken(secret)
	displayPrefix := secret
	if len(displayPrefix) > 12 {
		displayPrefix = displayPrefix[:12]
	}
	expiresAt := time.Now().UTC().AddDate(0, 0, input.ExpiresInDays)

	var item APIToken
	err = tx.QueryRow(ctx, `
		INSERT INTO api_tokens (user_id,organization_id,name,token_prefix,token_hash,expires_at)
		VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6)
		RETURNING id::text,name,token_prefix,organization_id::text,expires_at,last_used_at,created_at,revoked_at
	`, principal.UserID, principal.Organization.ID, input.Name, displayPrefix, tokenHash, expiresAt).Scan(
		&item.ID, &item.Name, &item.TokenPrefix, &item.OrganizationID,
		&item.ExpiresAt, &item.LastUsedAt, &item.CreatedAt, &item.RevokedAt,
	)
	if err != nil {
		return CreateAPITokenResult{}, mapAPITokenDatabaseError(err)
	}
	item.OrganizationName = principal.Organization.Name
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id)
		VALUES ($1::uuid,$2::uuid,'auth.api_token_create','api_token',$3,'创建个人 API Token',
		        jsonb_build_object('name',$4::text,'expires_at',$5::timestamptz,'token_prefix',$6::text),NULLIF($7,'')::inet,NULLIF($8,''))
	`, principal.UserID, principal.Organization.ID, item.ID, item.Name, item.ExpiresAt, item.TokenPrefix, ipAddress, requestID); err != nil {
		return CreateAPITokenResult{}, fmt.Errorf("audit api token creation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateAPITokenResult{}, fmt.Errorf("commit api token creation: %w", err)
	}
	return CreateAPITokenResult{Item: item, Token: secret}, nil
}

func (s *Service) ListAPITokens(ctx context.Context, principal Principal, page pagination.Query) (pagination.Result[APIToken], error) {
	if principal.UserID == "" || principal.SessionID == "" || principal.AuthenticationType != AuthenticationSession {
		return pagination.Result[APIToken]{}, ErrSessionRequired
	}
	rows, err := s.pool.Query(ctx, `
		SELECT t.id::text,t.name,t.token_prefix,t.organization_id::text,o.organization_name,
		       t.expires_at,t.last_used_at,t.created_at,t.revoked_at,count(*) OVER()
		FROM api_tokens t
		JOIN organizations o ON o.id=t.organization_id
		WHERE t.user_id=$1::uuid
		ORDER BY t.created_at DESC,t.id DESC
		LIMIT $2 OFFSET $3
	`, principal.UserID, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[APIToken]{}, fmt.Errorf("list api tokens: %w", err)
	}
	defer rows.Close()

	items := make([]APIToken, 0)
	total := 0
	for rows.Next() {
		var item APIToken
		if err := rows.Scan(&item.ID, &item.Name, &item.TokenPrefix, &item.OrganizationID, &item.OrganizationName, &item.ExpiresAt, &item.LastUsedAt, &item.CreatedAt, &item.RevokedAt, &total); err != nil {
			return pagination.Result[APIToken]{}, fmt.Errorf("scan api token: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[APIToken]{}, fmt.Errorf("iterate api tokens: %w", err)
	}
	if pagination.NeedsTotalFallback(len(items), page) {
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM api_tokens WHERE user_id=$1::uuid`, principal.UserID).Scan(&total); err != nil {
			return pagination.Result[APIToken]{}, fmt.Errorf("count api tokens: %w", err)
		}
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) RevokeAPIToken(ctx context.Context, principal Principal, tokenID, requestID, ipAddress string) error {
	if principal.UserID == "" || principal.SessionID == "" || principal.AuthenticationType != AuthenticationSession {
		return ErrSessionRequired
	}
	tokenID = strings.TrimSpace(tokenID)
	if !apiTokenUUIDPattern.MatchString(tokenID) {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin api token revoke: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var name, organizationID, tokenPrefix string
	err = tx.QueryRow(ctx, `
		UPDATE api_tokens SET revoked_at=now()
		WHERE id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL
		RETURNING name,organization_id::text,token_prefix
	`, tokenID, principal.UserID).Scan(&name, &organizationID, &tokenPrefix)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAPITokenNotFound
	}
	if err != nil {
		return fmt.Errorf("revoke api token: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id)
		VALUES ($1::uuid,$2::uuid,'auth.api_token_revoke','api_token',$3,'撤销个人 API Token',
		        jsonb_build_object('name',$4::text,'token_prefix',$5::text),NULLIF($6,'')::inet,NULLIF($7,''))
	`, principal.UserID, organizationID, tokenID, name, tokenPrefix, ipAddress, requestID); err != nil {
		return fmt.Errorf("audit api token revoke: %w", err)
	}
	return tx.Commit(ctx)
}

// AuthenticateAPIToken resolves a bearer token to the same principal model used
// by browser sessions. The token remains bound to the organization selected at
// creation time and becomes invalid if that appointment is removed or disabled.
func (s *Service) AuthenticateAPIToken(ctx context.Context, token string) (Principal, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, apiTokenPrefix) || len(token) <= len(apiTokenPrefix) {
		return Principal{}, ErrUnauthorized
	}
	tokenHash := hashToken(token)
	var user Principal
	var organizationID string
	err := s.pool.QueryRow(ctx, `
		SELECT u.id::text,u.username,u.display_name,COALESCE(u.email,''),COALESCE(u.mobile_phone,''),COALESCE(u.office_phone,''),u.must_change_password,
		       t.id::text,t.organization_id::text
		FROM api_tokens t
		JOIN users u ON u.id=t.user_id
		JOIN organizations o ON o.id=t.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
		JOIN user_organizations uo ON uo.user_id=t.user_id AND uo.organization_id=t.organization_id
		WHERE t.token_hash=$1 AND t.revoked_at IS NULL AND t.expires_at > now()
		  AND u.deleted_at IS NULL AND u.status='enabled' AND u.must_change_password=false
	`, tokenHash).Scan(&user.UserID, &user.Username, &user.DisplayName, &user.Email, &user.MobilePhone, &user.OfficePhone, &user.MustChangePassword, &user.APITokenID, &organizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrUnauthorized
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate api token: %w", err)
	}
	user.AuthenticationType = AuthenticationAPIToken
	if err := s.loadPrincipalDetails(ctx, &user, organizationID); err != nil {
		return Principal{}, err
	}
	if user.Organization == nil || user.Organization.ID != organizationID {
		return Principal{}, ErrUnauthorized
	}
	_, _ = s.pool.Exec(ctx, `UPDATE api_tokens SET last_used_at=now() WHERE id=$1::uuid AND (last_used_at IS NULL OR last_used_at < now()-interval '5 minutes')`, user.APITokenID)
	return user, nil
}

func mapAPITokenDatabaseError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrAPITokenNameConflict
	}
	return fmt.Errorf("write api token: %w", err)
}
