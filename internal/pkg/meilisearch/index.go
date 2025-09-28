package meilisearch

// Index represents the configuration for a MeiliSearch index, including its name, primary key,
// and lists of searchable, filterable, and sortable attributes.
type Index struct {
	// Name is the unique name of the MeiliSearch index.
	Name string
	// PrimaryKey is the field used as the primary key for documents in the index.
	PrimaryKey string
	// Searchable is a list of attributes that are searchable in the index.
	Searchable []string
	// Filterable is a list of attributes that can be used for filtering queries.
	Filterable []any
	// Sortable is a list of attributes that can be used for sorting results.
	Sortable []string
}

const (
	// IndexJobLogs is the name of the MeiliSearch index for job logs.
	IndexJobLogs string = "job_logs"
)

// Indexes contains the configuration for all MeiliSearch indexes used in the application.
var Indexes map[string]*Index = map[string]*Index{
	IndexJobLogs: {
		Name:       IndexJobLogs,
		PrimaryKey: "id",
		Searchable: []string{"message"},
		Filterable: []any{
			"job_id",
			"workflow_id",
			"user_id",
			"sequence_num",
			"stream",
		},
		Sortable: []string{"sequence_num"},
	},
}
