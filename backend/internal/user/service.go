package user

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"netassets/internal/access"
	"netassets/internal/auth"
	"netassets/internal/pagination"
)

var (
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrInvalid      = errors.New("invalid input")
	usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{2,63}$`)
)

type Organization struct {
	ID     string `json:"id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

type RoleAssignment struct {
	ID             string `json:"id"`
	RoleID         string `json:"role_id"`
	RoleKey        string `json:"role_key"`
	RoleName       string `json:"role_name"`
	RoleScope      string `json:"role_scope"`
	OrganizationID string `json:"organization_id,omitempty"`
	DataScope      string `json:"data_scope"`
}

type Appointment struct {
	Organization Organization     `json:"organization"`
	IsPrimary    bool             `json:"is_primary"`
	Assignments  []RoleAssignment `json:"assignments"`
}

type User struct {
	ID                 string           `json:"id"`
	Username           string           `json:"username"`
	DisplayName        string           `json:"display_name"`
	Email              string           `json:"email,omitempty"`
	MobilePhone        string           `json:"mobile_phone,omitempty"`
	OfficePhone        string           `json:"office_phone,omitempty"`
	MustChangePassword bool             `json:"must_change_password"`
	Status             string           `json:"status"`
	OrganizationID     string           `json:"organization_id,omitempty"`
	OrganizationCode   string           `json:"organization_code,omitempty"`
	OrganizationName   string           `json:"organization_name,omitempty"`
	RoleKey            string           `json:"role_key,omitempty"`
	RoleName           string           `json:"role_name,omitempty"`
	Appointments       []Appointment    `json:"appointments"`
	GlobalRoles        []RoleAssignment `json:"global_roles"`
	LastLoginAt        *time.Time       `json:"last_login_at"`
	CreatedAt          time.Time        `json:"created_at"`
}

type AssignmentInput struct {
	RoleID    string `json:"role_id"`
	DataScope string `json:"data_scope"`
}

type AppointmentInput struct {
	OrganizationID string            `json:"organization_id"`
	IsPrimary      bool              `json:"is_primary"`
	Assignments    []AssignmentInput `json:"assignments"`
}

type CreateInput struct {
	Username      string             `json:"username"`
	Password      string             `json:"password"`
	DisplayName   string             `json:"display_name"`
	Email         string             `json:"email"`
	MobilePhone   string             `json:"mobile_phone"`
	OfficePhone   string             `json:"office_phone"`
	Appointments  []AppointmentInput `json:"appointments"`
	GlobalRoleIDs []string           `json:"global_role_ids"`
	// Legacy fields remain accepted so existing API callers keep working.
	OrganizationID string `json:"organization_id"`
	RoleKey        string `json:"role_key"`
}

type UpdateInput struct {
	DisplayName   string             `json:"display_name"`
	Email         string             `json:"email"`
	MobilePhone   string             `json:"mobile_phone"`
	OfficePhone   string             `json:"office_phone"`
	Appointments  []AppointmentInput `json:"appointments"`
	GlobalRoleIDs []string           `json:"global_role_ids"`
}

type StatusInput struct {
	Status string `json:"status"`
}
type ResetPasswordInput struct {
	Password string `json:"password"`
}

type ListQuery struct {
	Keyword        string
	Status         string
	OrganizationID string
	RoleID         string
	Page           int
	PageSize       int
}

type ListResult struct {
	Items []User
	Total int
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func canManage(principal auth.Principal) bool { return principal.HasPermission("user.manage") }

func (s *Service) List(ctx context.Context, principal auth.Principal) ([]User, error) {
	result, err := s.Search(ctx, principal, ListQuery{Page: 1, PageSize: 100})
	return result.Items, err
}

// Search returns scoped users with lightweight server-side filters and pagination.
// Authorization filtering is applied before pagination so totals never reveal users
// outside a subsidiary administrator's management scope.
func (s *Service) Search(ctx context.Context, principal auth.Principal, query ListQuery) (ListResult, error) {
	if !canManage(principal) {
		return ListResult{}, ErrForbidden
	}
	query.Keyword = strings.TrimSpace(query.Keyword)
	query.Status = strings.ToLower(strings.TrimSpace(query.Status))
	query.OrganizationID = strings.TrimSpace(query.OrganizationID)
	query.RoleID = strings.TrimSpace(query.RoleID)
	if len(query.Keyword) > 128 || (query.Status != "" && query.Status != "enabled" && query.Status != "disabled" && query.Status != "locked") {
		return ListResult{}, ErrInvalid
	}
	if query.Page <= 0 {
		query.Page = 1
	}
	if query.PageSize <= 0 {
		query.PageSize = 20
	}
	if query.PageSize > 100 {
		query.PageSize = 100
	}

	keywordPattern := "%" + escapeLike(query.Keyword) + "%"
	rows, err := s.pool.Query(ctx, `
		SELECT u.id::text, u.username, u.display_name, COALESCE(u.email,''), COALESCE(u.mobile_phone,''), COALESCE(u.office_phone,''), u.must_change_password, u.status, u.last_login_at, u.created_at
		FROM users u
		WHERE u.deleted_at IS NULL
		  AND ($1 = '' OR u.username ILIKE $5 ESCAPE '\' OR u.display_name ILIKE $5 ESCAPE '\' OR COALESCE(u.email,'') ILIKE $5 ESCAPE '\' OR COALESCE(u.mobile_phone,'') ILIKE $5 ESCAPE '\' OR COALESCE(u.office_phone,'') ILIKE $5 ESCAPE '\'
		       OR EXISTS (SELECT 1 FROM user_organizations uok JOIN organizations ok ON ok.id=uok.organization_id WHERE uok.user_id=u.id AND (ok.organization_name ILIKE $5 ESCAPE '\' OR ok.organization_code ILIKE $5 ESCAPE '\')))
		  AND ($2 = '' OR u.status = $2)
		  AND ($3 = '' OR EXISTS (SELECT 1 FROM user_organizations uoo WHERE uoo.user_id=u.id AND uoo.organization_id::text=$3))
		  AND ($4 = '' OR EXISTS (SELECT 1 FROM user_roles urr WHERE urr.user_id=u.id AND urr.role_id::text=$4))
		ORDER BY u.username
	`, query.Keyword, query.Status, query.OrganizationID, query.RoleID, keywordPattern)
	if err != nil {
		return ListResult{}, fmt.Errorf("search users: %w", err)
	}
	defer rows.Close()

	offset := (query.Page - 1) * query.PageSize
	result := ListResult{Items: make([]User, 0, query.PageSize)}
	for rows.Next() {
		var item User
		if err := rows.Scan(&item.ID, &item.Username, &item.DisplayName, &item.Email, &item.MobilePhone, &item.OfficePhone, &item.MustChangePassword, &item.Status, &item.LastLoginAt, &item.CreatedAt); err != nil {
			return ListResult{}, fmt.Errorf("scan user: %w", err)
		}
		if err := s.loadUserRelations(ctx, &item); err != nil {
			return ListResult{}, err
		}
		if !principal.HasRole("group_admin") {
			allowed, err := s.targetWithinManagementScope(ctx, principal, item.ID)
			if err != nil {
				return ListResult{}, err
			}
			if !allowed {
				continue
			}
		}
		if result.Total >= offset && len(result.Items) < query.PageSize {
			result.Items = append(result.Items, item)
		}
		result.Total++
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, fmt.Errorf("iterate users: %w", err)
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, principal auth.Principal, userID string) (User, error) {
	if !canManage(principal) {
		return User{}, ErrForbidden
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return User{}, ErrInvalid
	}
	if !principal.HasRole("group_admin") {
		if err := s.ensureTargetManageable(ctx, principal, userID); err != nil {
			return User{}, err
		}
	}
	var item User
	err := s.pool.QueryRow(ctx, `
		SELECT u.id::text, u.username, u.display_name, COALESCE(u.email,''), COALESCE(u.mobile_phone,''), COALESCE(u.office_phone,''), u.must_change_password, u.status, u.last_login_at, u.created_at
		FROM users u WHERE u.id=$1::uuid AND u.deleted_at IS NULL
	`, userID).Scan(&item.ID, &item.Username, &item.DisplayName, &item.Email, &item.MobilePhone, &item.OfficePhone, &item.MustChangePassword, &item.Status, &item.LastLoginAt, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	if err := s.loadUserRelations(ctx, &item); err != nil {
		return User{}, err
	}
	return item, nil
}

// ListManageableOrganizations returns exactly the enabled non-group organizations
// for which the caller has a user.manage assignment at the relevant data scope.
func (s *Service) ListManageableOrganizations(ctx context.Context, principal auth.Principal) ([]Organization, error) {
	result, err := s.SearchManageableOrganizations(ctx, principal, pagination.Query{Page: 1, PageSize: pagination.MaxPageSize})
	return result.Items, err
}

func (s *Service) SearchManageableOrganizations(ctx context.Context, principal auth.Principal, page pagination.Query) (pagination.Result[Organization], error) {
	if !canManage(principal) {
		return pagination.Result[Organization]{}, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE descendant_roots(id) AS (
			SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled' JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='user.manage'
			WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope='organization_and_descendants'
		), descendants(id) AS (
			SELECT id FROM descendant_roots UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id=parent.id
		), accessible(id) AS (
			SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled' JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='user.manage'
			WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope IN ('organization','organization_and_descendants')
			UNION SELECT id FROM descendants
		)
		SELECT o.id::text,o.organization_code,o.organization_name,o.organization_type,o.status,count(*) OVER ()
		FROM organizations o WHERE o.status='enabled' AND o.organization_type<>'group' AND ($2::boolean OR o.id IN (SELECT id FROM accessible))
		ORDER BY o.organization_code LIMIT $3 OFFSET $4`, principal.UserID, principal.HasRole("group_admin"), page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[Organization]{}, fmt.Errorf("search manageable organizations: %w", err)
	}
	defer rows.Close()
	items := make([]Organization, 0, page.PageSize)
	total := 0
	for rows.Next() {
		var item Organization
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.Type, &item.Status, &total); err != nil {
			return pagination.Result[Organization]{}, fmt.Errorf("scan manageable organization: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Organization]{}, err
	}
	if pagination.NeedsTotalFallback(len(items), page) {
		if err := s.pool.QueryRow(ctx, `
            WITH RECURSIVE descendant_roots(id) AS (
                SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled' JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='user.manage'
                WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope='organization_and_descendants'
            ), descendants(id) AS (
                SELECT id FROM descendant_roots UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id=parent.id
            ), accessible(id) AS (
                SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled' JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='user.manage'
                WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope IN ('organization','organization_and_descendants')
                UNION SELECT id FROM descendants
            )
            SELECT count(*) FROM organizations o
            WHERE o.status='enabled' AND o.organization_type<>'group' AND ($2::boolean OR o.id IN (SELECT id FROM accessible))
        `, principal.UserID, principal.HasRole("group_admin")).Scan(&total); err != nil {
			return pagination.Result[Organization]{}, fmt.Errorf("count manageable organizations: %w", err)
		}
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, input CreateInput, requestID, ipAddress string) (User, error) {
	if !canManage(principal) {
		return User{}, ErrForbidden
	}
	input.Username = strings.TrimSpace(input.Username)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.TrimSpace(input.Email)
	input.MobilePhone = strings.TrimSpace(input.MobilePhone)
	input.OfficePhone = strings.TrimSpace(input.OfficePhone)
	if !usernamePattern.MatchString(input.Username) || len(input.Password) < 10 || input.DisplayName == "" || len(input.DisplayName) > 128 || len(input.Email) > 255 || len(input.MobilePhone) > 64 || len(input.OfficePhone) > 64 {
		return User{}, ErrInvalid
	}
	if err := s.compatibilityAppointments(ctx, &input); err != nil {
		return User{}, err
	}
	if err := normalizeAppointments(&input.Appointments); err != nil {
		return User{}, err
	}
	if err := normalizeGlobalRoleIDs(&input.GlobalRoleIDs); err != nil {
		return User{}, err
	}
	if err := s.validateManagementInput(ctx, principal, input.Appointments, input.GlobalRoleIDs); err != nil {
		return User{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("begin create user: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.validateAppointments(ctx, tx, input.Appointments); err != nil {
		return User{}, err
	}
	if err := s.validateGlobalRoles(ctx, tx, input.GlobalRoleIDs); err != nil {
		return User{}, err
	}
	var item User
	err = tx.QueryRow(ctx, `INSERT INTO users (username,password_hash,display_name,email,mobile_phone,office_phone,status,password_changed_at,must_change_password) VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),'enabled',NULL,true) RETURNING id::text,username,display_name,COALESCE(email,''),COALESCE(mobile_phone,''),COALESCE(office_phone,''),must_change_password,status,last_login_at,created_at`, input.Username, string(hash), input.DisplayName, input.Email, input.MobilePhone, input.OfficePhone).Scan(&item.ID, &item.Username, &item.DisplayName, &item.Email, &item.MobilePhone, &item.OfficePhone, &item.MustChangePassword, &item.Status, &item.LastLoginAt, &item.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return User{}, ErrConflict
		}
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	if err := replaceAppointments(ctx, tx, item.ID, input.Appointments, input.GlobalRoleIDs); err != nil {
		return User{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs (user_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES ($1::uuid,'user.create','user',$2,'新增用户',jsonb_build_object('username',$3::text),NULLIF($4,'')::inet,NULLIF($5,''))`, principal.UserID, item.ID, item.Username, ipAddress, requestID); err != nil {
		return User{}, fmt.Errorf("audit create user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit create user: %w", err)
	}
	if err := s.loadUserRelations(ctx, &item); err != nil {
		return User{}, err
	}
	return item, nil
}

func (s *Service) Update(ctx context.Context, principal auth.Principal, userID string, input UpdateInput, requestID, ipAddress string) (User, error) {
	if !canManage(principal) {
		return User{}, ErrForbidden
	}
	userID = strings.TrimSpace(userID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.TrimSpace(input.Email)
	input.MobilePhone = strings.TrimSpace(input.MobilePhone)
	input.OfficePhone = strings.TrimSpace(input.OfficePhone)
	if userID == "" || input.DisplayName == "" || len(input.DisplayName) > 128 || len(input.Email) > 255 || len(input.MobilePhone) > 64 || len(input.OfficePhone) > 64 {
		return User{}, ErrInvalid
	}
	if err := normalizeAppointments(&input.Appointments); err != nil {
		return User{}, err
	}
	if err := normalizeGlobalRoleIDs(&input.GlobalRoleIDs); err != nil {
		return User{}, err
	}
	if userID == principal.UserID {
		return User{}, ErrForbidden
	}
	if err := s.validateManagementInput(ctx, principal, input.Appointments, input.GlobalRoleIDs); err != nil {
		return User{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("begin update user: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.ensureTargetManageable(ctx, principal, userID); err != nil {
		return User{}, err
	}
	if err := s.validateAppointments(ctx, tx, input.Appointments); err != nil {
		return User{}, err
	}
	if err := s.validateGlobalRoles(ctx, tx, input.GlobalRoleIDs); err != nil {
		return User{}, err
	}
	if err := s.ensureGroupAdminRetained(ctx, tx, userID, input.GlobalRoleIDs); err != nil {
		return User{}, err
	}
	var item User
	err = tx.QueryRow(ctx, `UPDATE users SET display_name=$2,email=NULLIF($3,''),mobile_phone=NULLIF($4,''),office_phone=NULLIF($5,''),updated_at=now() WHERE id=$1::uuid AND deleted_at IS NULL RETURNING id::text,username,display_name,COALESCE(email,''),COALESCE(mobile_phone,''),COALESCE(office_phone,''),must_change_password,status,last_login_at,created_at`, userID, input.DisplayName, input.Email, input.MobilePhone, input.OfficePhone).Scan(&item.ID, &item.Username, &item.DisplayName, &item.Email, &item.MobilePhone, &item.OfficePhone, &item.MustChangePassword, &item.Status, &item.LastLoginAt, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("update user: %w", err)
	}
	if err := replaceAppointments(ctx, tx, userID, input.Appointments, input.GlobalRoleIDs); err != nil {
		return User{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions s SET current_organization_id=uo.organization_id FROM user_organizations uo WHERE s.user_id=$1::uuid AND uo.user_id=s.user_id AND uo.is_primary AND (s.current_organization_id IS NULL OR NOT EXISTS (SELECT 1 FROM user_organizations current_uo WHERE current_uo.user_id=s.user_id AND current_uo.organization_id=s.current_organization_id))`, userID); err != nil {
		return User{}, fmt.Errorf("repair working organizations: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE user_id=$1::uuid AND revoked_at IS NULL`, userID); err != nil {
		return User{}, fmt.Errorf("revoke user sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs (user_id,action,resource_type,resource_id,summary,request_ip,request_id) VALUES ($1::uuid,'user.update','user',$2,'编辑用户及多组织任职',NULLIF($3,'')::inet,NULLIF($4,''))`, principal.UserID, userID, ipAddress, requestID); err != nil {
		return User{}, fmt.Errorf("audit update user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit update user: %w", err)
	}
	if err := s.loadUserRelations(ctx, &item); err != nil {
		return User{}, err
	}
	return item, nil
}

func (s *Service) UpdateStatus(ctx context.Context, principal auth.Principal, userID string, input StatusInput, requestID, ipAddress string) error {
	if !canManage(principal) {
		return ErrForbidden
	}
	userID = strings.TrimSpace(userID)
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if userID == "" || (input.Status != "enabled" && input.Status != "disabled") {
		return ErrInvalid
	}
	if userID == principal.UserID {
		return ErrForbidden
	}
	if err := s.ensureTargetManageable(ctx, principal, userID); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin update user status: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM users WHERE id=$1::uuid AND deleted_at IS NULL FOR UPDATE`, userID).Scan(&currentStatus); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("load user status: %w", err)
	}
	if currentStatus == "enabled" && input.Status != "enabled" {
		if err := lockGroupAdminMutation(ctx, tx); err != nil {
			return err
		}
		var admins int
		if err := tx.QueryRow(ctx, `SELECT count(DISTINCT ur.user_id) FROM user_roles ur JOIN roles r ON r.id=ur.role_id JOIN users u ON u.id=ur.user_id WHERE r.role_key='group_admin' AND u.status='enabled' AND u.deleted_at IS NULL`).Scan(&admins); err != nil {
			return fmt.Errorf("count group admins: %w", err)
		}
		if admins <= 1 {
			return ErrInvalid
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET status=$2,updated_at=now() WHERE id=$1::uuid`, userID, input.Status); err != nil {
		return fmt.Errorf("set user status: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE user_id=$1::uuid AND revoked_at IS NULL`, userID); err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs (user_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES ($1::uuid,'user.status','user',$2,'变更用户状态',jsonb_build_object('status',$3::text),NULLIF($4,'')::inet,NULLIF($5,''))`, principal.UserID, userID, input.Status, ipAddress, requestID); err != nil {
		return fmt.Errorf("audit user status: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *Service) ResetPassword(ctx context.Context, principal auth.Principal, userID string, input ResetPasswordInput, requestID, ipAddress string) error {
	if !canManage(principal) {
		return ErrForbidden
	}
	userID = strings.TrimSpace(userID)
	if userID == "" || len(input.Password) < 10 {
		return ErrInvalid
	}
	if userID == principal.UserID {
		return ErrForbidden
	}
	if err := s.ensureTargetManageable(ctx, principal, userID); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reset password: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2,password_changed_at=NULL,must_change_password=true,updated_at=now() WHERE id=$1::uuid AND deleted_at IS NULL`, userID, string(hash))
	if err != nil {
		return fmt.Errorf("reset password: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE user_id=$1::uuid AND revoked_at IS NULL`, userID); err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs (user_id,action,resource_type,resource_id,summary,request_ip,request_id) VALUES ($1::uuid,'user.password_reset','user',$2,'重置用户密码',NULLIF($3,'')::inet,NULLIF($4,''))`, principal.UserID, userID, ipAddress, requestID); err != nil {
		return fmt.Errorf("audit password reset: %w", err)
	}
	return tx.Commit(ctx)
}

// validateManagementInput ensures non-group administrators cannot create or
// move a user outside of the organizations for which their exact role grants
// user.manage. Group roles are intentionally a group-administrator-only action.
func (s *Service) validateManagementInput(ctx context.Context, principal auth.Principal, appointments []AppointmentInput, globalRoleIDs []string) error {
	if principal.HasRole("group_admin") {
		return nil
	}
	if len(globalRoleIDs) > 0 {
		return ErrForbidden
	}
	for _, appointment := range appointments {
		allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, appointment.OrganizationID, "user.manage")
		if err != nil {
			return fmt.Errorf("check organization user management scope: %w", err)
		}
		if !allowed {
			return ErrForbidden
		}
	}
	return nil
}

func (s *Service) ensureTargetManageable(ctx context.Context, principal auth.Principal, userID string) error {
	if principal.HasRole("group_admin") {
		return nil
	}
	allowed, err := s.targetWithinManagementScope(ctx, principal, userID)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

// targetWithinManagementScope requires every existing appointment to be in scope
// and rejects group-role users. This prevents a subsidiary administrator from
// overwriting another subsidiary's appointment during an edit.
func (s *Service) targetWithinManagementScope(ctx context.Context, principal auth.Principal, userID string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1::uuid AND deleted_at IS NULL)`, userID).Scan(&exists); err != nil {
		return false, fmt.Errorf("find target user: %w", err)
	}
	if !exists {
		return false, ErrNotFound
	}
	var hasGroupRole bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id
			WHERE ur.user_id=$1::uuid AND ur.organization_id IS NULL AND r.role_scope='group'
		)
	`, userID).Scan(&hasGroupRole); err != nil {
		return false, fmt.Errorf("check target group role: %w", err)
	}
	if hasGroupRole {
		return false, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT organization_id::text FROM user_organizations WHERE user_id=$1::uuid`, userID)
	if err != nil {
		return false, fmt.Errorf("load target appointments: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var organizationID string
		if err := rows.Scan(&organizationID); err != nil {
			return false, fmt.Errorf("scan target appointment: %w", err)
		}
		count++
		allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, organizationID, "user.manage")
		if err != nil {
			return false, err
		}
		if !allowed {
			return false, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate target appointments: %w", err)
	}
	return count > 0, nil
}

func (s *Service) compatibilityAppointments(ctx context.Context, input *CreateInput) error {
	if len(input.Appointments) > 0 {
		return nil
	}
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.RoleKey = strings.TrimSpace(input.RoleKey)
	if input.OrganizationID == "" || input.RoleKey == "" {
		return ErrInvalid
	}
	var roleID string
	if err := s.pool.QueryRow(ctx, `SELECT id::text FROM roles WHERE role_key=$1 AND role_scope='organization' AND status='enabled'`, input.RoleKey).Scan(&roleID); errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalid
	} else if err != nil {
		return fmt.Errorf("load legacy role: %w", err)
	}
	input.Appointments = []AppointmentInput{{OrganizationID: input.OrganizationID, IsPrimary: true, Assignments: []AssignmentInput{{RoleID: roleID, DataScope: "organization"}}}}
	return nil
}

func normalizeAppointments(items *[]AppointmentInput) error {
	if len(*items) == 0 {
		return ErrInvalid
	}
	primary := 0
	seenOrgs := map[string]bool{}
	for i := range *items {
		item := &(*items)[i]
		item.OrganizationID = strings.TrimSpace(item.OrganizationID)
		if item.OrganizationID == "" || seenOrgs[item.OrganizationID] {
			return ErrInvalid
		}
		seenOrgs[item.OrganizationID] = true
		if item.IsPrimary {
			primary++
		}
		seenRoles := map[string]bool{}
		clean := make([]AssignmentInput, 0, len(item.Assignments))
		for _, assignment := range item.Assignments {
			assignment.RoleID = strings.TrimSpace(assignment.RoleID)
			assignment.DataScope = strings.ToLower(strings.TrimSpace(assignment.DataScope))
			if assignment.DataScope == "" {
				assignment.DataScope = "organization"
			}
			if assignment.RoleID == "" || seenRoles[assignment.RoleID] || !validDataScope(assignment.DataScope) {
				return ErrInvalid
			}
			seenRoles[assignment.RoleID] = true
			clean = append(clean, assignment)
		}
		if len(clean) == 0 {
			return ErrInvalid
		}
		sort.Slice(clean, func(a, b int) bool { return clean[a].RoleID < clean[b].RoleID })
		item.Assignments = clean
	}
	if primary != 1 {
		return ErrInvalid
	}
	return nil
}
func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func validDataScope(value string) bool {
	return value == "organization" || value == "organization_and_descendants" || value == "owned"
}

func normalizeGlobalRoleIDs(ids *[]string) error {
	seen := map[string]bool{}
	clean := make([]string, 0, len(*ids))
	for _, id := range *ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
		clean = append(clean, id)
	}
	sort.Strings(clean)
	*ids = clean
	return nil
}

func (s *Service) validateGlobalRoles(ctx context.Context, tx pgx.Tx, roleIDs []string) error {
	for _, roleID := range roleIDs {
		var scope, status string
		if err := tx.QueryRow(ctx, `SELECT role_scope,status FROM roles WHERE id=$1::uuid`, roleID).Scan(&scope, &status); errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalid
		} else if err != nil {
			return fmt.Errorf("load global role: %w", err)
		}
		if scope != "group" || status != "enabled" {
			return ErrInvalid
		}
	}
	return nil
}

// lockGroupAdminMutation serializes group-administrator removals and account
// disabling, so two concurrent updates cannot leave the system without one.
func lockGroupAdminMutation(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('netassets:group_admin'))`); err != nil {
		return fmt.Errorf("lock group administrator mutation: %w", err)
	}
	return nil
}

// ensureGroupAdminRetained prevents an update from removing the only enabled
// group administrator. Disabling that account is protected by UpdateStatus too.
func (s *Service) ensureGroupAdminRetained(ctx context.Context, tx pgx.Tx, userID string, globalRoleIDs []string) error {
	if err := lockGroupAdminMutation(ctx, tx); err != nil {
		return err
	}
	var isEnabled, currentlyGroupAdmin bool
	if err := tx.QueryRow(ctx, `
		SELECT u.status = 'enabled', EXISTS(
			SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id
			WHERE ur.user_id=u.id AND ur.organization_id IS NULL AND r.role_key='group_admin'
		)
		FROM users u WHERE u.id=$1::uuid AND u.deleted_at IS NULL FOR UPDATE
	`, userID).Scan(&isEnabled, &currentlyGroupAdmin); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("load current group administrator assignment: %w", err)
	}
	if !isEnabled || !currentlyGroupAdmin {
		return nil
	}
	var retainGroupAdmin bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM roles WHERE id = ANY($1::uuid[]) AND role_key='group_admin')`, globalRoleIDs).Scan(&retainGroupAdmin); err != nil {
		return fmt.Errorf("check group administrator assignment: %w", err)
	}
	if retainGroupAdmin {
		return nil
	}
	var adminCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(DISTINCT ur.user_id)
		FROM user_roles ur
		JOIN roles r ON r.id=ur.role_id
		JOIN users u ON u.id=ur.user_id
		WHERE ur.organization_id IS NULL AND r.role_key='group_admin'
		  AND r.status='enabled' AND u.status='enabled' AND u.deleted_at IS NULL
	`).Scan(&adminCount); err != nil {
		return fmt.Errorf("count group administrators: %w", err)
	}
	if adminCount <= 1 {
		return ErrInvalid
	}
	return nil
}

func (s *Service) validateAppointments(ctx context.Context, tx pgx.Tx, appointments []AppointmentInput) error {
	for _, appointment := range appointments {
		var organizationType, status string
		if err := tx.QueryRow(ctx, `SELECT organization_type,status FROM organizations WHERE id=$1::uuid`, appointment.OrganizationID).Scan(&organizationType, &status); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return fmt.Errorf("load appointment organization: %w", err)
		}
		if organizationType == "group" || status != "enabled" {
			return ErrInvalid
		}
		for _, assignment := range appointment.Assignments {
			var roleScope, status string
			if err := tx.QueryRow(ctx, `SELECT role_scope,status FROM roles WHERE id=$1::uuid`, assignment.RoleID).Scan(&roleScope, &status); errors.Is(err, pgx.ErrNoRows) {
				return ErrInvalid
			} else if err != nil {
				return fmt.Errorf("load appointment role: %w", err)
			}
			if roleScope != "organization" || status != "enabled" {
				return ErrInvalid
			}
		}
	}
	return nil
}

func replaceAppointments(ctx context.Context, tx pgx.Tx, userID string, appointments []AppointmentInput, globalRoleIDs []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1::uuid`, userID); err != nil {
		return fmt.Errorf("clear user role assignments: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_organizations WHERE user_id=$1::uuid`, userID); err != nil {
		return fmt.Errorf("clear user appointments: %w", err)
	}
	for _, roleID := range globalRoleIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO user_roles (user_id,role_id,organization_id,data_scope) VALUES ($1::uuid,$2::uuid,NULL,'all')`, userID, roleID); err != nil {
			return fmt.Errorf("assign global user role: %w", err)
		}
	}
	for _, appointment := range appointments {
		if _, err := tx.Exec(ctx, `INSERT INTO user_organizations (user_id,organization_id,is_primary) VALUES ($1::uuid,$2::uuid,$3)`, userID, appointment.OrganizationID, appointment.IsPrimary); err != nil {
			return fmt.Errorf("create user appointment: %w", err)
		}
		for _, assignment := range appointment.Assignments {
			if _, err := tx.Exec(ctx, `INSERT INTO user_roles (user_id,role_id,organization_id,data_scope) VALUES ($1::uuid,$2::uuid,$3::uuid,$4)`, userID, assignment.RoleID, appointment.OrganizationID, assignment.DataScope); err != nil {
				return fmt.Errorf("assign user role: %w", err)
			}
		}
	}
	return nil
}

func (s *Service) loadUserRelations(ctx context.Context, item *User) error {
	rows, err := s.pool.Query(ctx, `SELECT o.id::text,o.organization_code,o.organization_name,o.organization_type,uo.is_primary FROM user_organizations uo JOIN organizations o ON o.id=uo.organization_id WHERE uo.user_id=$1::uuid ORDER BY uo.is_primary DESC,o.organization_code`, item.ID)
	if err != nil {
		return fmt.Errorf("load user appointments: %w", err)
	}
	defer rows.Close()
	appointments := make([]Appointment, 0)
	for rows.Next() {
		var appointment Appointment
		if err := rows.Scan(&appointment.Organization.ID, &appointment.Organization.Code, &appointment.Organization.Name, &appointment.Organization.Type, &appointment.IsPrimary); err != nil {
			return fmt.Errorf("scan user appointment: %w", err)
		}
		assignments, err := s.loadAssignments(ctx, item.ID, appointment.Organization.ID)
		if err != nil {
			return err
		}
		appointment.Assignments = assignments
		appointments = append(appointments, appointment)
		if appointment.IsPrimary {
			item.OrganizationID = appointment.Organization.ID
			item.OrganizationCode = appointment.Organization.Code
			item.OrganizationName = appointment.Organization.Name
			if len(assignments) > 0 {
				item.RoleKey = assignments[0].RoleKey
				item.RoleName = assignments[0].RoleName
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate user appointments: %w", err)
	}
	item.Appointments = appointments
	global, err := s.loadAssignments(ctx, item.ID, "")
	if err != nil {
		return err
	}
	item.GlobalRoles = global
	if item.RoleKey == "" && len(global) > 0 {
		item.RoleKey = global[0].RoleKey
		item.RoleName = global[0].RoleName
	}
	return nil
}
func (s *Service) loadAssignments(ctx context.Context, userID, organizationID string) ([]RoleAssignment, error) {
	rows, err := s.pool.Query(ctx, `SELECT ur.id::text,ur.role_id::text,r.role_key,r.role_name,r.role_scope,COALESCE(ur.organization_id::text,''),ur.data_scope FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=$1::uuid AND (($2='' AND ur.organization_id IS NULL) OR ur.organization_id::text=$2) ORDER BY r.role_name`, userID, organizationID)
	if err != nil {
		return nil, fmt.Errorf("load role assignments: %w", err)
	}
	defer rows.Close()
	items := make([]RoleAssignment, 0)
	for rows.Next() {
		var item RoleAssignment
		if err := rows.Scan(&item.ID, &item.RoleID, &item.RoleKey, &item.RoleName, &item.RoleScope, &item.OrganizationID, &item.DataScope); err != nil {
			return nil, fmt.Errorf("scan role assignment: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate role assignments: %w", err)
	}
	return items, nil
}
