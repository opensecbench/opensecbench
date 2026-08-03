package action

import "testing"

func TestValidateScriptSafety(t *testing.T) {
	cases := []struct {
		name    string
		a       Action
		wantErr bool
	}{
		{
			name:    "shell with templated subject field is rejected",
			a:       Action{Kind: KindScript, Image: "img", Cmd: []string{"sh", "-c", "scan {{subject.location}}"}},
			wantErr: true,
		},
		{
			name:    "bash with templated title is rejected",
			a:       Action{Kind: KindScript, Image: "img", Cmd: []string{"/bin/bash", "-c", "echo {{subject.title}}"}},
			wantErr: true,
		},
		{
			name:    "shell referencing the env var is allowed",
			a:       Action{Kind: KindScript, Image: "img", Cmd: []string{"sh", "-c", "scan \"$OSB_SUBJECT_LOCATION\""}},
			wantErr: false,
		},
		{
			name:    "plain argv with templated field is allowed (exec argv, not shell)",
			a:       Action{Kind: KindScript, Image: "img", Cmd: []string{"semgrep", "--config=auto", "{{subject.location}}"}},
			wantErr: false,
		},
		{
			name:    "non-shell interpreter with template is allowed",
			a:       Action{Kind: KindScript, Image: "img", Cmd: []string{"python", "scan.py", "{{subject.id}}"}},
			wantErr: false,
		},
		{
			name:    "agent action is unaffected",
			a:       Action{Kind: KindAgent, ProfileID: "p", Instruction: "look at {{subject.title}}"},
			wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateScriptSafety(tc.a)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateScriptSafety() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}
