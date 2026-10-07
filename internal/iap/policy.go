package iap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var emailPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._%+\-]*@[a-z0-9][a-z0-9.\-]*\.[a-z]{2,}$`)
var domainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)

type Allowlist struct {
	accounts map[string]bool
	domains  map[string]bool
}

func ParseAllowlist(data []byte) (Allowlist, error) {
	if len(data) > 64*1024 {
		return Allowlist{}, errors.New("allow_accounts.yaml is too large")
	}
	var raw struct {
		Accounts []string `yaml:"accounts"`
		Domains  []string `yaml:"domains"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return Allowlist{}, fmt.Errorf("parse allow_accounts.yaml: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Allowlist{}, errors.New("allow_accounts.yaml must contain one document")
	}
	list := Allowlist{accounts: map[string]bool{}, domains: map[string]bool{}}
	for _, value := range raw.Accounts {
		value = strings.ToLower(strings.TrimSpace(value))
		if !emailPattern.MatchString(value) || list.accounts[value] {
			return Allowlist{}, fmt.Errorf("invalid or duplicate account %q", value)
		}
		list.accounts[value] = true
	}
	for _, value := range raw.Domains {
		value = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "@")
		if !domainPattern.MatchString(value) || list.domains[value] {
			return Allowlist{}, fmt.Errorf("invalid or duplicate domain %q", value)
		}
		list.domains[value] = true
	}
	if len(list.accounts)+len(list.domains) == 0 {
		return Allowlist{}, errors.New("allow_accounts.yaml must allow at least one account or domain")
	}
	return list, nil
}

func (l Allowlist) Allows(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if !emailPattern.MatchString(email) {
		return false
	}
	if l.accounts[email] {
		return true
	}
	_, domain, _ := strings.Cut(email, "@")
	return l.domains[domain]
}

func (l Allowlist) Members() []string {
	members := make([]string, 0, len(l.accounts)+len(l.domains))
	for email := range l.accounts {
		members = append(members, "user:"+email)
	}
	for domain := range l.domains {
		members = append(members, "domain:"+domain)
	}
	sort.Strings(members)
	return members
}

func ReconcilePolicy(existing []byte, list Allowlist) ([]byte, error) {
	var policy map[string]json.RawMessage
	if err := json.Unmarshal(existing, &policy); err != nil {
		return nil, fmt.Errorf("parse IAP IAM policy: %w", err)
	}
	if policy == nil {
		return nil, errors.New("empty IAP IAM policy")
	}
	var bindings []map[string]json.RawMessage
	if raw := policy["bindings"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &bindings); err != nil {
			return nil, fmt.Errorf("parse IAP IAM bindings: %w", err)
		}
	}
	kept := make([]map[string]json.RawMessage, 0, len(bindings)+1)
	for _, binding := range bindings {
		var role string
		if err := json.Unmarshal(binding["role"], &role); err != nil {
			return nil, fmt.Errorf("parse IAP IAM role: %w", err)
		}
		if role != "roles/iap.httpsResourceAccessor" {
			kept = append(kept, binding)
		}
	}
	members := list.Members()
	if len(members) == 0 {
		return nil, errors.New("empty IAP allowlist")
	}
	memberData, _ := json.Marshal(members)
	kept = append(kept, map[string]json.RawMessage{
		"role":    json.RawMessage(`"roles/iap.httpsResourceAccessor"`),
		"members": memberData,
	})
	bindingsData, err := json.Marshal(kept)
	if err != nil {
		return nil, err
	}
	policy["bindings"] = bindingsData
	return json.MarshalIndent(policy, "", "  ")
}
