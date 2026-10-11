// Package inventorydraft centralises the draft status vocabulary shared by the
// asset and business system working inventories and by inventory tasks.
//
// Two tables carry drafts: inventory_asset_drafts and
// inventory_business_system_drafts. They also cover two lifecycles — the
// organization working version (task_id IS NULL) and the task submission
// draft (task_id IS NOT NULL) — so status predicates used to be repeated as
// inline SQL literals in ~80 places. A wrong literal silently widened or
// narrowed visibility, so the semantically distinct predicates live here.
package inventorydraft

// Draft lifecycle states. These values are stored in inventory_*_drafts.
const (
	Editing   = "editing"
	Submitted = "submitted"
	Returned  = "returned"
	Published = "published"
	Discarded = "discarded"
)

// SQL predicates. Each one has a distinct meaning and must not be swapped for
// another: "active" and "editable" differ by Submitted, and Published must
// never be folded into the active list because PostgreSQL cannot infer the
// partial unique index from ON CONFLICT ... WHERE.
const (
	// ActiveSQL keeps editing, submitted and returned drafts. Used whenever a
	// working version must survive as the authoritative view of a row: list
	// visibility, ledger shadowing, uniqueness reservation, diff snapshots.
	ActiveSQL = `('editing','submitted','returned')`

	// EditableSQL keeps only drafts the organization may still change. Submitted
	// drafts are locked for review and Published/Discarded are terminal.
	EditableSQL = `('editing','returned')`

	// ReviewableSQL keeps drafts that already carry a review decision, used to
	// resolve the published row behind a task submission.
	ReviewableSQL = `('submitted','published')`
)

// ActiveStatuses and EditableStatuses are the Go equivalents, for code that
// compares a scanned draft_status instead of building SQL.
func ActiveStatuses() []string   { return []string{Editing, Submitted, Returned} }
func EditableStatuses() []string { return []string{Editing, Returned} }
