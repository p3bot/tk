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

// Edge is one depends/related relationship. FromPath ties it to the owning file for replace-on-reconcile.
type Edge struct {
	FromPath  string
	FromID    string
	FromScope string
	ToID      string
	ToScope   string
	Kind      string // EdgeDepends or EdgeRelated
}

// Edge kind values stored on edges.kind.
const (
	EdgeDepends = "depends"
	EdgeRelated = "related"
)
