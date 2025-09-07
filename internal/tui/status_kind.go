package tui

// StatusKind indicates severity for status messages/spinners.
type StatusKind int

const (
	StatusInfo StatusKind = iota
	StatusSuccess
	StatusWarn
	StatusError
)
