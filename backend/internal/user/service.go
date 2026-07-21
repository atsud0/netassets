package user

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"netassets/internal/auth"
)

var (
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrInvalid      = errors.New("invalid input")
	usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{2,63}$`)
)

type User struct {
	ID               string     `json:"id"`
	Username         string     `json:"username"`
	DisplayName      string     `json:"display_name"`
	Email            string     `json:"email,omitempty"`
	Status           string     `json:"status"`
	OrganizationID   string     `json:"organization_id,omitempty"`
	OrganizationCode string     `json:"organization_code,omitempty"`
	OrganizationName string     `json:"organization_name,omitempty"`
	RoleKey          string     `json:"role_key,omitempty"`
	RoleName         string     `json:"role_name,omitempty"`
	LastLoginAt      *time.Time `json:"last_login_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

type CreateInput struct {
	Username       string `json:"username"`
	Password       string `json:"password"`
	DisplayName    string `json:"display_name"`
	Email          string `json:"email"`
	OrganizationID string `json:"organization_id"`
	RoleKey        string `json:"role_key"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) List(ctx context.Context, principal auth.Principal) ([]User, error) {
	if !principal.HasRole("group_admin") {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
        SELECT u.id::text, u.username, u.display_name, COALESCE(u.email, ''), u.status,
               COALESCE(o.id::text, ''), COALESCE(o.organization_code, ''), COALESCE(o.organization_name, ''),
               COALESCE(r.role_key, ''), COALESCE(r.role_name, ''), u.last_login_at, u.created_at
        FROM users u
        LEFT JOIN LATERAL (
            SELECT org.id, org.organization_code, org.organization_name
            FROM user_organizations uo
            JOIN organizations org ON org.id = uo.organization_id
            WHERE uo.user_id = u.id
            ORDER BY uo.is_primary DESC, uo.created_at
            LIMIT 1
        ) o ON true
        LEFT JOIN LATERAL (
            SELECT role.role_key, role.role_name
            FROM user_roles ur
            JOIN roles role ON role.id = ur.role_id
            WHERE ur.user_id = u.id
            ORDER BY CASE WHEN role.role_key = 'group_admin' THEN 0 ELSE 1 END, role.role_key
            LIMIT 1
        ) r ON true
        WHERE u.deleted_at IS NULL
        ORDER BY CASE WHEN r.role_key = 'group_admin' THEN 0 ELSE 1 END, o.organization_code, u.username
    `)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	items := make([]User, 0)
	for rows.Next() {
		var item User
		if err := rows.Scan(&item.ID, &item.Username, &item.DisplayName, &item.Email, &item.Status,
			&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName, &item.RoleKey, &item.RoleName,
			&item.LastLoginAt, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return items, nil
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, input CreateInput, requestID, ipAddress string) (User, error) {
	if !principal.HasRole("group_admin") {
		return User{}, ErrForbidden
	}
	input.Username = strings.TrimSpace(input.Username)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.TrimSpace(input.Email)
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.RoleKey = strings.TrimSpace(input.RoleKey)
	if !usernamePattern.MatchString(input.Username) || len(input.Password) < 10 || input.DisplayName == "" || len(input.DisplayName) > 128 || input.OrganizationID == "" {
		return User{}, ErrInvalid
	}
	if input.RoleKey != "organization_admin" && input.RoleKey != "organization_editor" {
		return User{}, ErrInvalid
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
	if err != nil {
		return User{}, fmt.Errorf("hash user password: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("begin create user: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var organizationCode, organizationName, organizationType, organizationStatus string
	if err := tx.QueryRow(ctx, `SELECT organization_code, organization_name, organization_type, status FROM organizations WHERE id = $1::uuid`, input.OrganizationID).
		Scan(&organizationCode, &organizationName, &organizationType, &organizationStatus); errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	} else if err != nil {
		return User{}, fmt.Errorf("load user organization: %w", err)
	}
	if organizationStatus != "enabled" || organizationType == "group" {
		return User{}, ErrInvalid
	}

	var roleID, roleName string
	if err := tx.QueryRow(ctx, `SELECT id::text, role_name FROM roles WHERE role_key = $1`, input.RoleKey).Scan(&roleID, &roleName); errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrInvalid
	} else if err != nil {
		return User{}, fmt.Errorf("load user role: %w", err)
	}

	var item User
	err = tx.QueryRow(ctx, `
        INSERT INTO users (username, password_hash, display_name, email, status, password_changed_at)
        VALUES ($1, $2, $3, NULLIF($4, ''), 'enabled', now())
        RETURNING id::text, username, display_name, COALESCE(email, ''), status, last_login_at, created_at
    `, input.Username, string(hash), input.DisplayName, input.Email).Scan(
		&item.ID, &item.Username, &item.DisplayName, &item.Email, &item.Status, &item.LastLoginAt, &item.CreatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return User{}, ErrConflict
		}
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_organizations (user_id, organization_id, is_primary) VALUES ($1::uuid, $2::uuid, true)`, item.ID, input.OrganizationID); err != nil {
		return User{}, fmt.Errorf("attach user organization: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_roles (user_id, role_id, organization_id, data_scope) VALUES ($1::uuid, $2::uuid, $3::uuid, 'organization')`, item.ID, roleID, input.OrganizationID); err != nil {
		return User{}, fmt.Errorf("assign user role: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
        VALUES ($1::uuid, $2::uuid, 'user.create', 'user', $3, '新增组织用户',
                jsonb_build_object('username', $4::text, 'role', $5::text), NULLIF($6, '')::inet, NULLIF($7, ''))
    `, principal.UserID, input.OrganizationID, item.ID, item.Username, input.RoleKey, ipAddress, requestID); err != nil {
		return User{}, fmt.Errorf("write user audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit user: %w", err)
	}
	item.OrganizationID, item.OrganizationCode, item.OrganizationName = input.OrganizationID, organizationCode, organizationName
	item.RoleKey, item.RoleName = input.RoleKey, roleName
	return item, nil
}
