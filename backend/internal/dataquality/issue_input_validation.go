package dataquality

// IssueValidationError exposes only an actionable, public issue-input
// message. Database errors and unclassified ErrInvalid retain safe fallbacks.
type IssueValidationError struct {
	Message string
}

func (e *IssueValidationError) Error() string { return e.Message }
func (e *IssueValidationError) Unwrap() error { return ErrInvalid }

func invalidIssue(message string) error { return &IssueValidationError{Message: message} }
