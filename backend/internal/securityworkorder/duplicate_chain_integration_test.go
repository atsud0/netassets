package securityworkorder

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
)

// A duplicate relationship is evidence: it asserts "this is the same issue as
// that one". The service layer has the rules, but nothing pinned them against a
// migrated database, so a chain, a self-link, a forged status or a silent
// acceptance of a stale write could all land unnoticed. This test exercises the
// dedicated action, the two generic write paths that must never create one, and
// the concurrent case where two duplicates race for the same master.
func TestDuplicateFindingChainIsConstrained(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("NETASSETS_TEST_DATABASE_URL is not set; integration test requires a dedicated migrated PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `TRUNCATE organizations, users CASCADE`); err != nil {
		t.Fatalf("reset duplicate fixtures: %v", err)
	}

	var groupID, organizationID, userID string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (organization_code,organization_name,organization_type)
		VALUES ('DUP_CHAIN_GROUP','重复漏洞链路测试集团','group') RETURNING id::text`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (parent_id,organization_code,organization_name,organization_type)
		VALUES ($1::uuid,'DUP_CHAIN_SUB','重复漏洞链路测试子公司','subsidiary') RETURNING id::text`, groupID).Scan(&organizationID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (username,password_hash,display_name)
		VALUES ('dup_chain_operator','not-used','重复漏洞链路测试人员') RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	authorized := auth.Principal{UserID: userID, Organization: &auth.Organization{ID: groupID},
		Roles: []string{auth.RoleSecurityOperator}, Permissions: []string{auth.PermissionSecurityFinding}}
	unauthorized := auth.Principal{UserID: userID, Organization: &auth.Organization{ID: organizationID}}
	service := NewService(pool)

	var systemID, assetID string
	if err := pool.QueryRow(ctx, `INSERT INTO business_systems
		(system_code,system_name,owner_organization_id,system_status,system_level,internet_exposure,created_by,updated_by)
		VALUES ('DUP_CHAIN_SYS','重复链路测试系统',$1::uuid,'running','normal','unknown',$2::uuid,$2::uuid) RETURNING id::text`,
		organizationID, userID).Scan(&systemID); err != nil {
		t.Fatalf("insert business system: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO business_system_data_security_profiles (business_system_id) VALUES ($1::uuid)`, systemID); err != nil {
		t.Fatalf("insert data security profile: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO assets
		(asset_type,asset_value,normalized_value,asset_name,environment,network_scope,status,importance,source,created_by,updated_by)
		VALUES ('ip','192.0.2.240','192.0.2.240','重复链路测试资产','production','private','active','normal','manual',$1::uuid,$1::uuid)
		RETURNING id::text`, userID).Scan(&assetID); err != nil {
		t.Fatalf("insert asset: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO asset_organizations (asset_id,organization_id,relationship_type,is_primary)
		VALUES ($1::uuid,$2::uuid,'owner',true)`, assetID, organizationID); err != nil {
		t.Fatalf("assign asset: %v", err)
	}

	var code int
	insert := func(title, status string) string {
		t.Helper()
		code++
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO security_findings
			(finding_code,title,severity,status,assignee_organization_id,asset_id,business_system_id,description,source,created_by,updated_by)
			VALUES ($1::text,$2::text,'high',$3::text,$4::uuid,$5::uuid,$6::uuid,'重复链路验证','manual',$7::uuid,$7::uuid)
			RETURNING id::text`, "DUP-CHAIN-"+time.Now().Format("150405.000000")+"-"+strings.Repeat("0", 0)+itoa(code), title, status, organizationID, assetID, systemID, userID).Scan(&id); err != nil {
			t.Fatalf("insert finding %s: %v", title, err)
		}
		return id
	}
	row := func(id string) (status, master string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT status, COALESCE(duplicate_of_finding_id::text,'') FROM security_findings WHERE id=$1::uuid`, id).
			Scan(&status, &master); err != nil {
			t.Fatalf("read finding %s: %v", id, err)
		}
		return status, master
	}
	version := func(id string) *time.Time {
		t.Helper()
		var value time.Time
		if err := pool.QueryRow(ctx, `SELECT updated_at FROM security_findings WHERE id=$1::uuid`, id).Scan(&value); err != nil {
			t.Fatalf("read updated_at: %v", err)
		}
		return &value
	}

	t.Run("a duplicate points at a canonical master", func(t *testing.T) {
		master := insert("主漏洞", "confirmed")
		source := insert("重复漏洞", "confirmed")
		if _, err := service.LinkDuplicateFinding(ctx, authorized, source, DuplicateFindingInput{
			MasterFindingID: master, Reason: "同一资产的同一问题", ExpectedUpdatedAt: version(source),
		}, "dup-chain-request", "127.0.0.1"); err != nil {
			t.Fatalf("LinkDuplicateFinding: %v", err)
		}
		status, master2 := row(source)
		if status != "duplicate" || master2 != master {
			t.Fatalf("after link status=%q master=%q, want duplicate/%s", status, master2, master)
		}
		if status, master3 := row(master); status != "confirmed" || master3 != "" {
			t.Fatalf("the master was modified: status=%q master=%q", status, master3)
		}
		var audits int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='security_finding.link_duplicate' AND resource_id=$1`, source).Scan(&audits); err != nil {
			t.Fatalf("count duplicate audits: %v", err)
		}
		if audits != 1 {
			t.Fatalf("link_duplicate audits = %d, want exactly 1", audits)
		}
	})

	t.Run("a duplicate cannot be relinked or turned into a chain", func(t *testing.T) {
		other := insert("另一个主漏洞", "confirmed")
		source := insert("重复漏洞二", "confirmed")
		if _, err := service.LinkDuplicateFinding(ctx, authorized, source, DuplicateFindingInput{
			MasterFindingID: other, Reason: "首次关联", ExpectedUpdatedAt: version(source),
		}, "dup-chain-first", "127.0.0.1"); err != nil {
			t.Fatalf("LinkDuplicateFinding: %v", err)
		}
		// A second link on the same finding would overwrite the first evidence.
		if _, err := service.LinkDuplicateFinding(ctx, authorized, source, DuplicateFindingInput{
			MasterFindingID: other, Reason: "改指向", ExpectedUpdatedAt: version(source),
		}, "dup-chain-second", "127.0.0.1"); !errors.Is(err, ErrState) {
			t.Fatalf("relinking an existing duplicate = %v, want ErrState", err)
		}
		// A third finding pointing at the already-duplicate finding would make a
		// chain: the master would no longer be canonical.
		third := insert("链尾漏洞", "confirmed")
		if _, err := service.LinkDuplicateFinding(ctx, authorized, third, DuplicateFindingInput{
			MasterFindingID: source, Reason: "指向重复项", ExpectedUpdatedAt: version(third),
		}, "dup-chain-tail", "127.0.0.1"); !errors.Is(err, ErrState) {
			t.Fatalf("chaining through a duplicate = %v, want ErrState", err)
		}
		if status, master := row(third); status != "confirmed" || master != "" {
			t.Fatalf("a refused chain still wrote status=%q master=%q", status, master)
		}
	})

	t.Run("a finding cannot be its own master", func(t *testing.T) {
		self := insert("自指漏洞", "confirmed")
		if _, err := service.LinkDuplicateFinding(ctx, authorized, self, DuplicateFindingInput{
			MasterFindingID: self, Reason: "自指", ExpectedUpdatedAt: version(self),
		}, "dup-chain-self", "127.0.0.1"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("self link = %v, want ErrInvalid", err)
		}
		// The database constraint must hold even if a future write path forgets
		// the service check.
		if _, err := pool.Exec(ctx, `UPDATE security_findings SET duplicate_of_finding_id=id WHERE id=$1::uuid`, self); err == nil {
			t.Fatal("the database accepted a self-referencing duplicate")
		}
	})

	t.Run("a settled finding cannot be relabelled as a duplicate", func(t *testing.T) {
		for _, status := range []string{"resolved", "fixed", "false_positive", "ignored", "risk_accepted"} {
			master := insert("主漏洞-"+status, "confirmed")
			settled := insert("已处置漏洞-"+status, status)
			if _, err := service.LinkDuplicateFinding(ctx, authorized, settled, DuplicateFindingInput{
				MasterFindingID: master, Reason: "事后改判", ExpectedUpdatedAt: version(settled),
			}, "dup-chain-"+status, "127.0.0.1"); !errors.Is(err, ErrState) {
				t.Fatalf("duplicating a %s finding = %v, want ErrState", status, err)
			}
		}
	})

	t.Run("a duplicate master cannot be closed out either", func(t *testing.T) {
		master := insert("可关闭主漏洞", "confirmed")
		source := insert("指向主漏洞的重复", "confirmed")
		if _, err := service.LinkDuplicateFinding(ctx, authorized, source, DuplicateFindingInput{
			MasterFindingID: master, Reason: "重复", ExpectedUpdatedAt: version(source),
		}, "dup-chain-master", "127.0.0.1"); err != nil {
			t.Fatalf("LinkDuplicateFinding: %v", err)
		}
		// Marking a duplicate as a false positive would erase the relationship and
		// silently reintroduce the finding into the outstanding counts.
		if _, err := service.ReviewRisk(ctx, authorized, source, RiskReviewInput{Comment: "改为误报"}, "dup-chain-reclassify", "127.0.0.1"); err == nil {
			t.Fatal("a duplicate was allowed to take an unrelated risk review action")
		}
		if status, master := row(source); status != "duplicate" || master != master {
			t.Fatalf("a refused reclassification changed the finding to status=%q master=%q", status, master)
		}
	})

	t.Run("the generic write paths cannot forge a duplicate", func(t *testing.T) {
		master := insert("受保护主漏洞", "confirmed")
		target := insert("待编辑漏洞", "confirmed")
		// The update path must refuse to move the finding into duplicate, even
		// though findingstatus.CanTransition allows the status value.
		if _, err := service.UpdateFinding(ctx, authorized, target, FindingInput{
			Title: "待编辑漏洞", Severity: "high", Status: "duplicate",
			AssigneeOrganizationID: organizationID, AssetID: assetID, BusinessSystemID: systemID,
			Description: "尝试伪造重复", Source: "manual", DuplicateOfFindingID: master,
			ExpectedUpdatedAt: version(target),
		}, "dup-chain-update", "127.0.0.1"); !errors.Is(err, ErrConflict) {
			t.Fatalf("UpdateFinding to duplicate = %v, want ErrConflict", err)
		}
		if status, master2 := row(target); status != "confirmed" || master2 != "" {
			t.Fatalf("a refused update still wrote status=%q master=%q", status, master2)
		}
		// And so must the create path.
		if _, err := service.CreateFinding(ctx, authorized, FindingInput{
			Title: "新建重复", Severity: "high", Status: "duplicate",
			AssigneeOrganizationID: organizationID, AssetID: assetID, BusinessSystemID: systemID,
			Description: "尝试伪造重复", Source: "manual", DuplicateOfFindingID: master,
		}, "dup-chain-create", "127.0.0.1"); !errors.Is(err, ErrConflict) {
			t.Fatalf("CreateFinding as duplicate = %v, want ErrConflict", err)
		}
	})

	t.Run("a stale optimistic token cannot write the relationship", func(t *testing.T) {
		master := insert("并发主漏洞", "confirmed")
		source := insert("并发重复", "confirmed")
		stale := version(source)
		time.Sleep(2 * time.Millisecond)
		if _, err := service.UpdateFinding(ctx, authorized, source, FindingInput{
			Title: "并发重复-改描述", Severity: "high", Status: "confirmed",
			AssigneeOrganizationID: organizationID, AssetID: assetID, BusinessSystemID: systemID,
			Description: "别处刚刚改过这条漏洞", Source: "manual", ExpectedUpdatedAt: version(source),
		}, "dup-chain-bump", "127.0.0.1"); err != nil {
			t.Fatalf("UpdateFinding to bump the version: %v", err)
		}
		if _, err := service.LinkDuplicateFinding(ctx, authorized, source, DuplicateFindingInput{
			MasterFindingID: master, Reason: "用旧版本写入", ExpectedUpdatedAt: stale,
		}, "dup-chain-stale", "127.0.0.1"); !errors.Is(err, ErrEditConflict) {
			t.Fatalf("stale duplicate link = %v, want ErrEditConflict", err)
		}
		if status, master2 := row(source); status != "confirmed" || master2 != "" {
			t.Fatalf("a stale write still changed the finding to status=%q master=%q", status, master2)
		}
	})

	t.Run("a caller without the finding permission cannot link", func(t *testing.T) {
		master := insert("权限主漏洞", "confirmed")
		source := insert("权限重复", "confirmed")
		if _, err := service.LinkDuplicateFinding(ctx, unauthorized, source, DuplicateFindingInput{
			MasterFindingID: master, Reason: "越权尝试", ExpectedUpdatedAt: version(source),
		}, "dup-chain-forbidden", "127.0.0.1"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("unauthorized duplicate link = %v, want ErrForbidden", err)
		}
		if status, master2 := row(source); status != "confirmed" || master2 != "" {
			t.Fatalf("an unauthorized link still wrote status=%q master=%q", status, master2)
		}
	})

	t.Run("concurrent links onto one master settle exactly once", func(t *testing.T) {
		master := insert("并发共享主漏洞", "confirmed")
		const workers = 4
		ids := make([]string, workers)
		for i := range ids {
			ids[i] = insert("并发重复-"+itoa(i), "confirmed")
		}
		var wait sync.WaitGroup
		results := make([]error, workers)
		for i := range ids {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				_, results[index] = service.LinkDuplicateFinding(ctx, authorized, ids[index], DuplicateFindingInput{
					MasterFindingID: master, Reason: "并发关联", ExpectedUpdatedAt: version(ids[index]),
				}, "dup-chain-concurrent-"+itoa(index), "127.0.0.1")
			}(i)
		}
		wait.Wait()
		linked := 0
		for index, err := range results {
			if err != nil {
				t.Errorf("concurrent link %d failed: %v", index, err)
				continue
			}
			status, masterID := row(ids[index])
			if status != "duplicate" || masterID != master {
				t.Errorf("concurrent link %d wrote status=%q master=%q", index, status, masterID)
			}
			linked++
		}
		if linked != workers {
			t.Fatalf("linked %d of %d concurrent duplicates", linked, workers)
		}
		// One audit per link and no more: the shared advisory lock must not let a
		// retried or raced write produce a second audit for the same relationship.
		var audits int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='security_finding.link_duplicate' AND details->>'master_finding_id'=$1`, master).Scan(&audits); err != nil {
			t.Fatalf("count duplicate audits for the shared master: %v", err)
		}
		if audits != workers {
			t.Fatalf("duplicate link audits for one master = %d, want exactly %d", audits, workers)
		}
	})
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}
