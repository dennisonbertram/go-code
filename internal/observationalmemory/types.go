package observationalmemory

import "time"

// Mode controls how observational memory behaves at runtime.
type Mode string

const (
	ModeOff              Mode = "off"
	ModeAuto             Mode = "auto"
	ModeLocalCoordinator Mode = "local_coordinator"
)

type ScopeKey struct {
	TenantID       string `json:"tenant_id"`
	ConversationID string `json:"conversation_id"`
	AgentID        string `json:"agent_id"`
}

func (k ScopeKey) MemoryID() string {
	return k.TenantID + "|" + k.ConversationID + "|" + k.AgentID
}

type Config struct {
	ObserveMinTokens       int `json:"observe_min_tokens"`
	SnippetMaxTokens       int `json:"snippet_max_tokens"`
	ReflectThresholdTokens int `json:"reflect_threshold_tokens"`
}

func DefaultConfig() Config {
	return Config{
		ObserveMinTokens:       1200,
		SnippetMaxTokens:       900,
		ReflectThresholdTokens: 4000,
	}
}

type ObservationChunk struct {
	Seq              int64     `json:"seq"`
	Content          string    `json:"content"`
	TokenCount       int       `json:"token_count"`
	Importance       float64   `json:"importance,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	SourceStartIndex int64     `json:"source_start_index"`
	SourceEndIndex   int64     `json:"source_end_index"`
}

type Record struct {
	MemoryID                    string                `json:"memory_id"`
	Scope                       ScopeKey              `json:"scope"`
	Enabled                     bool                  `json:"enabled"`
	StateVersion                int64                 `json:"state_version"`
	LastObservedMessageIndex    int64                 `json:"last_observed_message_index"`
	ActiveObservations          []ObservationChunk    `json:"active_observations"`
	ActiveObservationTokens     int                   `json:"active_observation_tokens"`
	ActiveReflection            string                `json:"active_reflection"`
	ActiveReflectionTokens      int                   `json:"active_reflection_tokens"`
	LastReflectedObservationSeq int64                 `json:"last_reflected_observation_seq"`
	StructuredReflection        *StructuredReflection `json:"structured_reflection,omitempty"`
	Config                      Config                `json:"config"`
	CreatedAt                   time.Time             `json:"created_at"`
	UpdatedAt                   time.Time             `json:"updated_at"`
}

type Status struct {
	Mode                     Mode      `json:"mode"`
	MemoryID                 string    `json:"memory_id"`
	Scope                    ScopeKey  `json:"scope"`
	Enabled                  bool      `json:"enabled"`
	StateVersion             int64     `json:"state_version"`
	ObservationCount         int       `json:"observation_count"`
	ActiveObservationTokens  int       `json:"active_observation_tokens"`
	ReflectionPresent        bool      `json:"reflection_present"`
	ActiveReflectionTokens   int       `json:"active_reflection_tokens"`
	LastObservedMessageIndex int64     `json:"last_observed_message_index"`
	UpdatedAt                time.Time `json:"updated_at"`
}

type Operation struct {
	OperationID   string    `json:"operation_id"`
	MemoryID      string    `json:"memory_id"`
	RunID         string    `json:"run_id"`
	ToolCallID    string    `json:"tool_call_id"`
	ScopeSequence int64     `json:"scope_sequence"`
	OperationType string    `json:"operation_type"`
	Status        string    `json:"status"`
	PayloadJSON   string    `json:"payload_json"`
	ErrorText     string    `json:"error_text"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Marker struct {
	MarkerID          string    `json:"marker_id"`
	MemoryID          string    `json:"memory_id"`
	MarkerType        string    `json:"marker_type"`
	CycleID           string    `json:"cycle_id"`
	MessageIndexStart int64     `json:"message_index_start"`
	MessageIndexEnd   int64     `json:"message_index_end"`
	TokenCount        int       `json:"token_count"`
	PayloadJSON       string    `json:"payload_json"`
	CreatedAt         time.Time `json:"created_at"`
}

type TranscriptMessage struct {
	Index      int64  `json:"index"`
	Role       string `json:"role"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Content    string `json:"content,omitempty"`
}

type ObserveRequest struct {
	Scope      ScopeKey
	RunID      string
	ToolCallID string
	Messages   []TranscriptMessage
}

type ObserveResult struct {
	Status    Status `json:"status"`
	Observed  bool   `json:"observed"`
	Reflected bool   `json:"reflected"`
}

type ExportResult struct {
	Format  string `json:"format"`
	Content string `json:"content"`
	Bytes   int    `json:"bytes"`
	Status  Status `json:"status"`
}

type ReviewResult struct {
	Analysis  string    `json:"analysis"`
	Model     string    `json:"model"`
	Timestamp time.Time `json:"timestamp"`
}
