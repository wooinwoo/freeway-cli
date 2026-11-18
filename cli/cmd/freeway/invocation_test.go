package main

import "testing"

func TestSessionInvocationsAreCommandsNotPrompts(t *testing.T) {
	cases := []struct {
		args        []string
		mode, query string
		all, json   bool
	}{
		{nil, "new", "", false, false},
		{[]string{"continue"}, "continue", "", false, false}, {[]string{"-c"}, "continue", "", false, false},
		{[]string{"resume"}, "resume", "", false, false}, {[]string{"resume", "abc12345"}, "resume", "abc12345", false, false},
		{[]string{"resume", "My development session"}, "resume", "My development session", false, false},
		{[]string{"resume", "continue"}, "resume", "continue", false, false},
		{[]string{"resume", "sessions"}, "resume", "sessions", false, false},
		{[]string{"--resume"}, "resume", "", false, false}, {[]string{"-r", "abc"}, "resume", "abc", false, false},
		{[]string{"resume", "--last"}, "continue", "", false, false}, {[]string{"resume", "--all"}, "resume", "", true, false},
		{[]string{"sessions", "--all", "--json"}, "sessions", "", true, true},
		{[]string{"--url", "http://127.0.0.1:4141", "resume", "abc"}, "resume", "abc", false, false},
	}
	for _, test := range cases {
		o, err := parseInvocation(test.args)
		if err != nil || o.mode != test.mode || o.query != test.query || o.all != test.all || o.json != test.json {
			t.Fatalf("%v: %#v %v", test.args, o, err)
		}
	}
	for _, args := range [][]string{{"--session"}, {"--unknown"}, {"sessions", "unexpected"}} {
		if _, err := parseInvocation(args); err == nil {
			t.Fatalf("invalid invocation accepted: %v", args)
		}
	}
}
