package remediation

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
	"netassets/internal/auth"
	"netassets/internal/pagination"
)

type PersonInput struct {
	OrganizationID  string    `json:"organization_id"`
	SystemID        string    `json:"business_system_id"`
	VendorID        string    `json:"vendor_id"`
	ParticipantType string    `json:"participant_type"`
	Username        string    `json:"username"`
	DisplayName     string    `json:"display_name"`
	Password        string    `json:"password"`
	ValidUntil      time.Time `json:"valid_until"`
}

func validatePerson(in PersonInput) error {
	if !validID(in.OrganizationID) || !validID(in.SystemID) || !usernamePattern.MatchString(in.Username) || !textOK(in.DisplayName, 128, true) || !in.ValidUntil.After(time.Now()) {
		return ErrInvalid
	}
	if in.ParticipantType != "internal" && in.ParticipantType != "external" {
		return ErrInvalid
	}
	if (in.ParticipantType == "external") != validID(in.VendorID) {
		return ErrInvalid
	}
	return nil
}
func checkSystem(ctx context.Context, tx pgx.Tx, org, system, vendor string) error {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM business_systems WHERE id=$1::uuid AND owner_organization_id=$2::uuid AND deleted_at IS NULL AND system_status<>'archived')
 AND ($3='' OR EXISTS(SELECT 1 FROM vendors WHERE id=NULLIF($3,'')::uuid AND organization_id=$2::uuid AND status='enabled' AND EXISTS(SELECT 1 FROM vendor_catalog c WHERE c.id=vendors.catalog_id AND c.status='enabled')))`, system, org, vendor).Scan(&ok)
	if err != nil {
		return dbError(err)
	}
	if !ok {
		return ErrInvalid
	}
	return nil
}
func createGrant(ctx context.Context, tx pgx.Tx, p auth.Principal, in PersonInput, userID string) (Object, error) {
	if in.VendorID != "" {
		tag, err := tx.Exec(ctx, `INSERT INTO vendor_memberships(vendor_id,user_id,valid_until) SELECT catalog_id,$2::uuid,$3 FROM vendors WHERE id=$1::uuid ON CONFLICT(vendor_id,user_id) DO UPDATE SET valid_until=GREATEST(vendor_memberships.valid_until,EXCLUDED.valid_until) WHERE vendor_memberships.status='enabled'`, in.VendorID, userID, in.ValidUntil)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() != 1 {
			return nil, ErrForbidden
		}
	}
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO remediation_grants(user_id,organization_id,business_system_id,vendor_id,participant_type,valid_until,sponsor_user_id)
 VALUES($1::uuid,$2::uuid,$3::uuid,NULLIF($4,'')::uuid,$5,$6,$7::uuid) RETURNING id::text`, userID, in.OrganizationID, in.SystemID, in.VendorID, in.ParticipantType, in.ValidUntil, p.UserID).Scan(&id)
	if err != nil {
		return nil, dbError(err)
	}
	if err = audit(ctx, tx, p, in.OrganizationID, id, "grant.create", Object{"before": nil, "after": Object{"user_id": userID, "business_system_id": in.SystemID, "valid_until": in.ValidUntil, "status": "enabled"}}); err != nil {
		return nil, err
	}
	return Object{"id": id, "user_id": userID}, nil
}

// CreatePerson creates a dedicated personal account. Existing identities are
// linked only after the owner accepts an invitation while authenticated.
func (s *Service) CreatePerson(ctx context.Context, p auth.Principal, in PersonInput) (Object, error) {
	if err := validatePerson(in); err != nil {
		return nil, err
	}
	if len(in.Password) < 10 || len(in.Password) > 72 {
		return nil, ErrInvalid
	}
	if err := allowed(ctx, s.pool, p, in.OrganizationID, "remediation.people.manage"); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = allowed(ctx, tx, p, in.OrganizationID, "remediation.people.manage"); err != nil {
		return nil, err
	}
	if err = checkSystem(ctx, tx, in.OrganizationID, in.SystemID, in.VendorID); err != nil {
		return nil, err
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name,must_change_password) VALUES($1,$2,$3,true) RETURNING id::text`, in.Username, string(hash), in.DisplayName).Scan(&id)
	if err != nil {
		return nil, dbError(err)
	}
	out, err := createGrant(ctx, tx, p, in, id)
	if err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}
func (s *Service) Invite(ctx context.Context, p auth.Principal, in PersonInput) (Object, error) {
	if err := validatePerson(in); err != nil {
		return nil, err
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = allowed(ctx, tx, p, in.OrganizationID, "remediation.people.manage"); err != nil {
		return nil, err
	}
	if err = checkSystem(ctx, tx, in.OrganizationID, in.SystemID, in.VendorID); err != nil {
		return nil, err
	}
	raw := token()
	var id string
	expires := time.Now().Add(48 * time.Hour)
	if in.ValidUntil.Before(expires) {
		expires = in.ValidUntil
	}
	err = tx.QueryRow(ctx, `INSERT INTO remediation_invitations(token_hash,organization_id,business_system_id,vendor_id,participant_type,intended_username,display_name,inviter_id,valid_until,expires_at)
 VALUES($1,$2::uuid,$3::uuid,NULLIF($4,'')::uuid,$5,$6,$7,$8::uuid,$9,$10) RETURNING id::text`, digest(raw), in.OrganizationID, in.SystemID, in.VendorID, in.ParticipantType, in.Username, in.DisplayName, p.UserID, in.ValidUntil, expires).Scan(&id)
	if err != nil {
		return nil, dbError(err)
	}
	if err = audit(ctx, tx, p, in.OrganizationID, id, "invitation.create", Object{"before": nil, "after": Object{"username": in.Username, "expires_at": expires, "status": "pending"}}); err != nil {
		return nil, err
	}
	return Object{"id": id, "token": raw, "expires_at": expires}, tx.Commit(ctx)
}

type AcceptInvitationInput struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

func (s *Service) Invitations(ctx context.Context, p auth.Principal, page pagination.Query) (pagination.Result[Object], error) {
	return pageObjects(ctx, s.pool, `SELECT i.id,i.intended_username,i.display_name,i.expires_at,i.accepted_at,i.revoked_at,i.valid_until,o.organization_name,b.system_name
 FROM remediation_invitations i JOIN organizations o ON o.id=i.organization_id JOIN business_systems b ON b.id=i.business_system_id
 WHERE remediation_can_manage($1::uuid,i.organization_id,'remediation.people.manage') ORDER BY i.created_at DESC,i.id`, page, p.UserID)
}
func (s *Service) RevokeInvitation(ctx context.Context, p auth.Principal, id, reason string) error {
	if !validID(id) || !textOK(reason, 2000, true) {
		return ErrInvalid
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var org string
	if err = tx.QueryRow(ctx, `SELECT organization_id::text FROM remediation_invitations WHERE id=$1::uuid AND accepted_at IS NULL AND revoked_at IS NULL FOR UPDATE`, id).Scan(&org); err != nil {
		return dbError(err)
	}
	if err = allowed(ctx, tx, p, org, "remediation.people.manage"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE remediation_invitations SET revoked_at=now() WHERE id=$1::uuid`, id); err != nil {
		return err
	}
	if err = audit(ctx, tx, p, org, id, "invitation.revoke", Object{"before": Object{"status": "pending"}, "after": Object{"status": "revoked"}, "reason": reason}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) AcceptInvitation(ctx context.Context, p *auth.Principal, in AcceptInvitationInput) (Object, error) {
	if len(in.Token) != 48 {
		return nil, ErrInvalid
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var person PersonInput
	var inviter, id string
	err = tx.QueryRow(ctx, `SELECT id::text,organization_id::text,business_system_id::text,COALESCE(vendor_id::text,''),participant_type,intended_username,display_name,inviter_id::text,valid_until
 FROM remediation_invitations WHERE token_hash=$1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at>now() FOR UPDATE`, digest(in.Token)).Scan(&id, &person.OrganizationID, &person.SystemID, &person.VendorID, &person.ParticipantType, &person.Username, &person.DisplayName, &inviter, &person.ValidUntil)
	if err != nil {
		return nil, dbError(err)
	}
	sponsor := auth.Principal{UserID: inviter}
	if err = allowed(ctx, tx, sponsor, person.OrganizationID, "remediation.people.manage"); err != nil {
		return nil, ErrForbidden
	}
	if err = checkSystem(ctx, tx, person.OrganizationID, person.SystemID, person.VendorID); err != nil {
		return nil, err
	}
	if !person.ValidUntil.After(time.Now()) {
		return nil, ErrInvalid
	}
	var userID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM users WHERE lower(username)=lower($1) AND deleted_at IS NULL`, person.Username).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		if p != nil {
			return nil, ErrInvalid
		}
		if len(in.Password) < 10 || len(in.Password) > 72 {
			return nil, ErrInvalid
		}
		hash, e := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if e != nil {
			return nil, e
		}
		err = tx.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name,must_change_password) VALUES($1,$2,$3,false) RETURNING id::text`, person.Username, string(hash), person.DisplayName).Scan(&userID)
	} else if err == nil && (p == nil || p.UserID != userID) {
		return nil, ErrForbidden
	}
	if err != nil {
		return nil, dbError(err)
	}
	out, err := createGrant(ctx, tx, sponsor, person, userID)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE remediation_invitations SET accepted_at=now() WHERE id=$1::uuid`, id); err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}
func (s *Service) People(ctx context.Context, p auth.Principal, org, system string, page pagination.Query) (pagination.Result[Object], error) {
	if org != "" && !validID(org) || system != "" && !validID(system) {
		return pagination.Result[Object]{}, ErrInvalid
	}
	return pageObjects(ctx, s.pool, `SELECT g.id,g.user_id,u.username,u.display_name,g.organization_id,o.organization_name,g.business_system_id,b.system_name,
 g.vendor_id,COALESCE(v.name,'') vendor_name,g.participant_type,g.status,g.valid_until,
 remediation_grant_valid(g.id,g.user_id,g.organization_id,g.business_system_id) available,
 (SELECT count(*) FROM remediation_tasks t WHERE t.assignee_user_id=g.user_id AND t.organization_id=g.organization_id AND t.status NOT IN ('completed','cancelled')) active_task_count
 FROM remediation_grants g JOIN users u ON u.id=g.user_id JOIN organizations o ON o.id=g.organization_id JOIN business_systems b ON b.id=g.business_system_id LEFT JOIN vendors v ON v.id=g.vendor_id
 WHERE (remediation_can_manage($1::uuid,g.organization_id,'remediation.people.manage') OR remediation_can_manage($1::uuid,g.organization_id,'remediation.assign'))
 AND ($2='' OR g.organization_id=NULLIF($2,'')::uuid) AND ($3='' OR g.business_system_id=NULLIF($3,'')::uuid)
 ORDER BY u.display_name,g.created_at DESC`, page, p.UserID, org, system)
}
func (s *Service) UpdateGrant(ctx context.Context, p auth.Principal, id, status string, until time.Time, reason string) error {
	if !validID(id) || (status != "enabled" && status != "revoked") || !textOK(reason, 2000, true) {
		return ErrInvalid
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var org string
	var currentUntil time.Time
	var currentStatus string
	if err = tx.QueryRow(ctx, `SELECT organization_id::text,valid_until,status FROM remediation_grants WHERE id=$1::uuid FOR UPDATE`, id).Scan(&org, &currentUntil, &currentStatus); err != nil {
		return dbError(err)
	}
	if err = allowed(ctx, tx, p, org, "remediation.people.manage"); err != nil {
		return err
	}
	if status == "enabled" {
		if !until.After(time.Now()) || until.Before(currentUntil) {
			return ErrInvalid
		}
	} else {
		until = currentUntil
	}
	// A renewal that cannot restore the vendor's access is worse than a refusal: the
	// page reports success, the grant row says "enabled", and the person still cannot
	// open their own tasks because a link further down the chain is broken. The vendor
	// is the only party who cannot repair that themselves.
	//
	// What counts as repairable is decided here, not guessed by the caller: an expired
	// membership is exactly what this renewal is for - the deadline extension below
	// pulls it forward. What is not repairable is a person who is no longer on the
	// vendor roster at all, or a vendor, catalog entry or account that has been
	// disabled, reassigned to another organization or deleted. Those need the 厂商 page
	// or an administrator, so the renewal is refused instead of silently reporting a
	// collaboration that nobody can work on.
	if status == "enabled" {
		var restorable bool
		if err = tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM remediation_grants g
			 JOIN users u ON u.id=g.user_id AND u.status='enabled' AND u.deleted_at IS NULL
			 JOIN organizations o ON o.id=g.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
			 JOIN business_systems b ON b.id=g.business_system_id AND b.owner_organization_id=g.organization_id AND b.deleted_at IS NULL AND b.system_status<>'archived'
			 JOIN vendors v ON v.id=g.vendor_id AND v.status='enabled' AND v.organization_id=g.organization_id
			 JOIN vendor_catalog c ON c.id=v.catalog_id AND c.status='enabled'
			 WHERE g.id=$1::uuid
			   AND EXISTS(SELECT 1 FROM vendor_memberships vm WHERE vm.vendor_id=c.id AND vm.user_id=g.user_id
			              AND vm.status='enabled' AND vm.valid_from<=$2::timestamptz))`, id, until).Scan(&restorable); err != nil {
			return err
		}
		if !restorable {
			return ErrConflict
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE remediation_grants SET status=$2,valid_until=$3 WHERE id=$1::uuid`, id, status, until); err != nil {
		return dbError(err)
	}
	if status == "enabled" {
		// The membership is the vendor's own record of when this person is available.
		// A renewal that leaves it behind would silently revoke the vendor one day
		// later, so the new grant deadline is pushed onto it as well. The start is
		// pulled forward for the same reason: a membership whose window has already
		// closed cannot be re-opened by extending its end alone.
		if _, err = tx.Exec(ctx, `UPDATE vendor_memberships m SET valid_from=LEAST(m.valid_from,now()),valid_until=GREATEST(m.valid_until,$2) FROM remediation_grants g JOIN vendors v ON v.id=g.vendor_id WHERE g.id=$1::uuid AND m.vendor_id=v.catalog_id AND m.user_id=g.user_id AND m.status='enabled'`, id, until); err != nil {
			return err
		}
	}
	if err = refreshInvalidOrders(ctx, tx); err != nil {
		return err
	}
	if err = audit(ctx, tx, p, org, id, "grant.update", Object{"before": Object{"status": currentStatus, "valid_until": currentUntil}, "after": Object{"status": status, "valid_until": until}, "reason": reason}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) Vendors(ctx context.Context, p auth.Principal, page pagination.Query) (pagination.Result[Object], error) {
	return pageObjects(ctx, s.pool, `SELECT v.id,v.catalog_id,v.name,v.organization_id,v.contact,v.status,c.status global_status,o.organization_name FROM vendors v JOIN vendor_catalog c ON c.id=v.catalog_id JOIN organizations o ON o.id=v.organization_id
 WHERE remediation_can_manage($1::uuid,v.organization_id,'vendor.manage') OR remediation_can_manage($1::uuid,v.organization_id,'remediation.people.manage') ORDER BY v.name`, page, p.UserID)
}
func (s *Service) SaveVendor(ctx context.Context, p auth.Principal, id, org, name, contact, status string) (Object, error) {
	if !textOK(name, 255, true) || !textOK(contact, 255, false) || !validID(org) || (status != "enabled" && status != "disabled") || (id != "" && !validID(id)) {
		return nil, ErrInvalid
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = allowed(ctx, tx, p, org, "vendor.manage"); err != nil {
		return nil, err
	}
	before := Object{}
	if id == "" {
		if !groupAdmin(ctx, tx, p) {
			return nil, ErrForbidden
		}
		var catalog string
		err = tx.QueryRow(ctx, `INSERT INTO vendor_catalog(name,created_by) VALUES(btrim($1),$2::uuid) ON CONFLICT(lower(btrim(name))) DO UPDATE SET name=vendor_catalog.name RETURNING id::text`, name, p.UserID).Scan(&catalog)
		if err != nil {
			return nil, dbError(err)
		}
		err = tx.QueryRow(ctx, `INSERT INTO vendors(name,organization_id,contact,status,created_by,catalog_id) VALUES($1,$2::uuid,$3,$4,$5::uuid,$6::uuid) RETURNING id::text`, strings.TrimSpace(name), org, contact, status, p.UserID, catalog).Scan(&id)
	} else {
		before, err = readObject(ctx, tx, `SELECT name,contact,status FROM vendors WHERE id=$1::uuid AND organization_id=$2::uuid`, id, org)
		if err != nil {
			return nil, err
		}
		if before["name"] != name {
			return nil, ErrInvalid
		}
		var returned string
		err = tx.QueryRow(ctx, `UPDATE vendors SET name=$3,contact=$4,status=$5 WHERE id=$1::uuid AND organization_id=$2::uuid RETURNING id::text`, id, org, name, contact, status).Scan(&returned)
	}
	if err != nil {
		return nil, dbError(err)
	}
	if err = refreshInvalidOrders(ctx, tx); err != nil {
		return nil, err
	}
	if err = audit(ctx, tx, p, org, id, "vendor.save", Object{"before": before, "after": Object{"name": name, "contact": contact, "status": status}}); err != nil {
		return nil, err
	}
	return Object{"id": id}, tx.Commit(ctx)
}
