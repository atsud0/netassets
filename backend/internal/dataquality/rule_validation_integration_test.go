package dataquality

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/auth"
)

func TestRuleValidationRejectsWithoutMutation(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("requires a dedicated migrated test database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	var userID string
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','规则边界测试') RETURNING id::text`, "rule_validation_"+stamp).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE user_id=$1::uuid`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1::uuid`, userID)
	}()
	svc := NewService(pool)
	p := auth.Principal{UserID: userID, Roles: []string{auth.RoleGroupAdmin}}
	for _, resource := range []string{"asset", "business_system"} {
		t.Run(resource, func(t *testing.T) {
			key := "validation_" + resource + "_" + stamp
			if _, err := pool.Exec(ctx, `INSERT INTO custom_field_definitions(resource_type,field_key,field_name,field_type,is_enabled) VALUES($1,$2,'规则边界字段','text',false)`, resource, key); err != nil {
				t.Fatal(err)
			}
			var ruleID string
			defer func() {
				_, _ = pool.Exec(ctx, `DELETE FROM data_quality_rules WHERE id=NULLIF($1,'')::uuid`, ruleID)
				_, _ = pool.Exec(ctx, `DELETE FROM custom_field_definitions WHERE resource_type=$1 AND field_key=$2`, resource, key)
			}()
			in := Input{RuleName: strings.Repeat("规", 128), ResourceType: resource, RuleType: "required", TargetField: "custom:" + key, Severity: "warning", Conditions: []RuleCondition{{Field: "custom:" + key, Operator: "not_empty"}}, SortOrder: 100000}
			created, err := svc.Create(ctx, p, in, "", "")
			if err != nil {
				t.Fatal(err)
			}
			ruleID = created.ID
			in.RuleName = strings.Repeat("R", 128)
			updated, err := svc.Update(ctx, p, ruleID, in, "", "")
			if err != nil || updated.RuleName != in.RuleName {
				t.Fatalf("update: %v", err)
			}
			snapshot := func() string {
				var raw string
				err := pool.QueryRow(ctx, `SELECT json_build_object('rule',(SELECT row_to_json(r) FROM data_quality_rules r WHERE id=$1::uuid),'audit',(SELECT json_agg(a ORDER BY id) FROM audit_logs a WHERE user_id=$2::uuid),'count',(SELECT count(*) FROM data_quality_rules))::text`, ruleID, userID).Scan(&raw)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			before := snapshot()
			base := in
			base.Conditions = append([]RuleCondition(nil), in.Conditions...)
			conditionField, conditionValue := "network_scope", "unknown_value"
			if resource == "business_system" {
				conditionField = "internet_exposure"
			}
			for _, tc := range []struct {
				change  func(*Input)
				message string
			}{
				{func(in *Input) { in.RuleName = "　 " }, "规则名称不能为空"},
				{func(in *Input) { in.RuleName = strings.Repeat("规", 129) }, "规则名称最多 128 字"},
				{func(in *Input) { in.TargetField = "custom:missing_" + stamp }, "请选择有效的必填检查项"},
				{func(in *Input) {
					in.Conditions = []RuleCondition{{Field: "custom:missing_" + stamp, Operator: "empty"}}
				}, "第 1 条生效条件请选择有效字段"},
				{func(in *Input) {
					in.Conditions = []RuleCondition{{Field: conditionField, Operator: "eq", Values: []string{conditionValue}}}
				}, "第 1 条生效条件：条件值必须选择当前字段支持的选项"},
			} {
				for _, method := range []string{"create", "update"} {
					bad := base
					bad.Conditions = append([]RuleCondition(nil), base.Conditions...)
					tc.change(&bad)
					var err error
					if method == "create" {
						_, err = svc.Create(ctx, p, bad, "", "")
					} else {
						_, err = svc.Update(ctx, p, ruleID, bad, "", "")
					}
					var validation *RuleValidationError
					if !errors.Is(err, ErrInvalid) || !errors.As(err, &validation) || validation.Message != tc.message {
						t.Fatalf("%s: %v", method, err)
					}
					if after := snapshot(); after != before {
						t.Fatalf("rejected %s mutated rule or audit", method)
					}
				}
			}
			// Database failures must not be disguised as user-input failures.
			closedPool, err := pgxpool.New(ctx, url)
			if err != nil {
				t.Fatal(err)
			}
			closedPool.Close()
			_, err = NewService(closedPool).Create(ctx, p, in, "", "")
			if err == nil || errors.Is(err, ErrInvalid) {
				t.Fatalf("database error converted to validation: %v", err)
			}
		})
	}
}
