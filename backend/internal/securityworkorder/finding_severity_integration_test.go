package securityworkorder

import (
	"context"
	"errors"
	"testing"

	"netassets/internal/pagination"
)

func TestFindingSeverityFilters(test *testing.T) {
	pool, manager, organization, originalID := reviewFixture(test)
	ctx := context.Background()
	service := NewService(pool, test.TempDir())
	original, err := service.GetFinding(ctx, manager, originalID)
	if err != nil {
		test.Fatal(err)
	}
	for _, severity := range []string{"critical", "high", "medium", "low", "info"} {
		_, err = service.CreateFinding(ctx, manager, FindingInput{Title: "级别 " + severity, Severity: severity, Status: "pending", AssigneeOrganizationID: organization, BusinessSystemID: original.BusinessSystemID, Description: "级别筛选", Source: "manual"}, "severity", "127.0.0.1")
		if err != nil {
			test.Fatal(err)
		}
	}
	for _, severity := range []string{"critical", "high", "medium", "low", "info"} {
		for _, pageNumber := range []int{1, 2} {
			result, queryErr := service.SearchFindings(ctx, manager, organization, "pending", "unlinked", pagination.Query{Page: pageNumber, PageSize: 100}, FindingSearchOptions{Severity: severity})
			if queryErr != nil || result.Total != 1 {
				test.Fatalf("severity %s page %d: %+v %v", severity, pageNumber, result, queryErr)
			}
			for _, finding := range result.Items {
				if finding.Severity != severity || finding.Status != "pending" || finding.AssigneeOrganizationID != organization {
					test.Fatalf("filters leaked: %+v", finding)
				}
			}
		}
	}
	otherOrg := p03InsertOrg(test, pool, "", "SEVERITY_OTHER", "其他组织", "subsidiary")
	otherUser := p03InsertUser(test, pool, "severity_other", "其他组织管理员")
	p03AssignRole(test, pool, otherUser, "organization_admin", otherOrg, "organization")
	outsider := p03Principal(test, pool, otherUser, []string{"organization_admin"})
	if _, err = service.SearchFindings(ctx, outsider, organization, "pending", "unlinked", pagination.Query{Page: 1, PageSize: 20}, FindingSearchOptions{Severity: "high"}); !errors.Is(err, ErrForbidden) {
		test.Fatalf("severity filter widened organization permissions: %v", err)
	}
	visible, err := service.SearchFindings(ctx, outsider, "", "pending", "unlinked", pagination.Query{Page: 1, PageSize: 20}, FindingSearchOptions{Severity: "high"})
	if err != nil || visible.Total != 0 {
		test.Fatalf("severity filter leaked findings: %+v %v", visible, err)
	}
	if _, err = service.SearchFindings(ctx, manager, "", "", "", pagination.Query{Page: 1, PageSize: 20}, FindingSearchOptions{Severity: "urgent"}); !errors.Is(err, ErrInvalid) {
		test.Fatalf("invalid severity: %v", err)
	}
}
