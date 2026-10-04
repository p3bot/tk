package index

// Ticket is one materialized ticket row (derived from a single file; Path is the physical key).
type Ticket struct {
	Path     string
	Scope    string
	ID       string // full id; on parse error, from the filename prefix
	ShortID  string
	Status   string
	OrderKey string
	Title    string
	Summary  string
	Created  string
	// Changed is the fence string, or empty when the key is absent.
	Changed string
	Tags    []string
	Custom  map[string]any
	// StatusConflict holds disputed terminal statuses from a merge conflict; empty otherwise.
	StatusConflict []string
	// Archived is true when the file lives under archive/.
	Archived bool
	// ParseError marks a quarantine row (id from filename; body still FTS-indexed).
	ParseError bool
	ParseMsg   string
	// SchemaError is true when a depends/related entry failed IsFullTicketID.
	SchemaError bool
	// Body populates FTS on write only; not stored as a column and not read back.
	Body    []byte
	MtimeNS int64
	Size    int64
}

// Design is one materialized design row. Path is the physical key. Body populates
// the design search index on write only and is not stored as a column.
type Design struct {
	Path    string
	Scope   string
	ID      string
	ShortID string
	Status  string
	Title   string
	Summary string
	Created string
	// Changed is the fence string, or empty when the key is absent.
	Changed string
	// ParseError marks a quarantine row (id from filename; raw file still indexed).
	ParseError bool
	ParseMsg   string
	Body       []byte
	MtimeNS    int64
	Size       int64
}

// Edge is one depends, related, or produces relationship. FromPath ties it to the owning file for replace-on-reconcile.
type Edge struct {
	FromPath  string
	FromID    string
	FromScope string
	ToID      string
	ToScope   string
	Kind      string // EdgeDepends, EdgeRelated, or EdgeProduces
}

// Edge kind values stored on edges.kind.
// EdgeProduces is also the design fence key those edges are read from.
const (
	EdgeDepends  = "depends"
	EdgeRelated  = "related"
	EdgeProduces = "produces"
)
