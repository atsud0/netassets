-- name: GetOrganization :one
SELECT * FROM organizations WHERE id = $1;

-- name: ListOrganizations :many
SELECT *
FROM organizations
WHERE ($1::uuid IS NULL OR parent_id = $1)
ORDER BY organization_name;
