package iap

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseAllowlist(t *testing.T) {
	list, err := ParseAllowlist([]byte("accounts:\n  - Reader@Example.com\ndomains:\n  - '@data-cloud.jp'\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		email string
		want  bool
	}{
		{"reader@example.com", true},
		{"someone@data-cloud.jp", true},
		{"SOMEONE@DATA-CLOUD.JP", true},
		{"someone@sub.data-cloud.jp", false},
		{"someone@other.jp", false},
	} {
		if got := list.Allows(tc.email); got != tc.want {
			t.Errorf("Allows(%q) = %t, want %t", tc.email, got, tc.want)
		}
	}
	want := []string{"domain:data-cloud.jp", "user:reader@example.com"}
	if got := list.Members(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Members() = %v, want %v", got, want)
	}
}

func TestReconcilePolicyReplacesOnlyIAPAccessMembers(t *testing.T) {
	list, err := ParseAllowlist([]byte("domains: [data-cloud.jp]"))
	if err != nil {
		t.Fatal(err)
	}
	input := []byte(`{"etag":"abc","version":3,"bindings":[{"role":"roles/iap.httpsResourceAccessor","members":["allAuthenticatedUsers"]},{"role":"roles/viewer","members":["user:admin@example.com"]}]}`)
	output, err := ReconcilePolicy(input, list)
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Etag     string `json:"etag"`
		Bindings []struct {
			Role    string   `json:"role"`
			Members []string `json:"members"`
		} `json:"bindings"`
	}
	if err := json.Unmarshal(output, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Etag != "abc" || len(policy.Bindings) != 2 || policy.Bindings[0].Role != "roles/viewer" ||
		policy.Bindings[1].Role != "roles/iap.httpsResourceAccessor" || !reflect.DeepEqual(policy.Bindings[1].Members, []string{"domain:data-cloud.jp"}) {
		t.Fatalf("policy: %+v", policy)
	}
}

func TestParseAllowlistRejectsUnsafeEntries(t *testing.T) {
	for _, input := range []string{
		"accounts: []\ndomains: []\n",
		"accounts: [allUsers]\n",
		"accounts: []\ndomains: ['*.data-cloud.jp']\n",
		"accounts: [user@example.com]\nunknown: yes\n",
		"accounts: [user@example.com, user@example.com]\n",
		strings.Repeat("a", 64*1024+1),
	} {
		if _, err := ParseAllowlist([]byte(input)); err == nil {
			t.Errorf("accepted %q", input[:min(len(input), 80)])
		}
	}
}
