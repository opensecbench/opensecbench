package analyst

// Agent playbooks (ADR-0019 §4). A playbook is a template — a goal and a set of steps, each a sub-task
// for a specialist profile with dependencies on other steps. Triggering one creates a plan (a DAG) that
// the runner executes in dependency order via delegation. Distinct from pkg/playbook (which sequences
// *capabilities*); an agent-playbook step may itself run capabilities through its profile's tools.
//
// Playbooks are goal + adaptable steps: each step's instruction tells the agent to read what's already
// known (KB, prior findings, the inventory) and do only what's needed, rather than a rigid script.

// PlaybookStep is one step of a playbook template.
type PlaybookStep struct {
	Key         string
	Profile     string
	Instruction string
	DependsOn   []string
}

// Playbook is a triggerable, engagement-shaped process.
type Playbook struct {
	ID          string
	Name        string
	Description string
	Goal        string
	Steps       []PlaybookStep
}

var builtinPlaybooks = []Playbook{
	{
		ID:          "onboarding",
		Name:        "Onboarding & inventory",
		Description: "The common start to any engagement: collect information and inventory the assets.",
		Goal:        "Establish a baseline understanding of the project — its assets, code, and attack surface.",
		Steps: []PlaybookStep{
			{
				Key:     "inventory",
				Profile: "code-analysis",
				Instruction: "Inventory this project. Use list_assets to see the source assets; for each " +
					"source_repo use list_dir and grep_code to summarize its languages, entry points, and " +
					"structure. First check the knowledge base and prior context for what's already known. " +
					"Write a concise inventory to inventory/summary.md in the workspace.",
			},
			{
				Key:     "surface",
				Profile: "code-analysis",
				Instruction: "Using the inventory, map the attack surface: authentication, key endpoints and " +
					"handlers, input boundaries, and sensitive components. Write your analysis to " +
					"analysis/surface.md in the workspace.",
				DependsOn: []string{"inventory"},
			},
			{
				Key:     "kickoff",
				Profile: "report-writer",
				Instruction: "Write a short engagement kickoff summary — what this project is, what was " +
					"inventoried, and the initial attack surface — from the workspace notes and the context " +
					"provided. Save it to reports/kickoff.md.",
				DependsOn: []string{"surface"},
			},
		},
	},
	{
		ID:          "recon",
		Name:        "Initial recon",
		Description: "Recon for a networked target: confirm scope, run baseline scans, and triage what's found.",
		Goal:        "Produce an initial recon picture of the in-scope targets and candidate areas to investigate.",
		Steps: []PlaybookStep{
			{
				Key:     "scope",
				Profile: "code-analysis",
				Instruction: "Review the project's assets and what is already known. Use list_assets, and check " +
					"the knowledge base (get_kb_entry) and prior findings so you don't repeat work. Summarize " +
					"the in-scope targets and the current state.",
			},
			{
				Key:     "scan",
				Profile: "pentester",
				Instruction: "Run appropriate baseline recon capabilities against the in-scope assets " +
					"(list_capabilities, then run_capability). Skip anything the knowledge base shows was " +
					"already done recently. Summarize what was found.",
				DependsOn: []string{"scope"},
			},
			{
				Key:     "triage",
				Profile: "report-writer",
				Instruction: "Summarize the recon results and list candidate areas worth deeper investigation. " +
					"Save it to reports/recon-summary.md.",
				DependsOn: []string{"scan"},
			},
		},
	},
}

// Playbooks returns the built-in agent playbooks.
func Playbooks() []Playbook { return builtinPlaybooks }

// PlaybookByID returns a playbook by id.
func PlaybookByID(id string) (Playbook, bool) {
	for _, p := range builtinPlaybooks {
		if p.ID == id {
			return p, true
		}
	}
	return Playbook{}, false
}
