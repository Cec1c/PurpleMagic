package launcherhub

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
)

// Optional signed client policy. No game admission, player data or NPC operations.
type AntiCheatPolicy struct {
	Enabled             bool
	Mode                string
	RequiredFiles       []AntiCheatFile   `json:"RequiredFiles,omitempty"`
	Rules               []AntiCheatRule   `json:"Rules,omitempty"`
	AllowedModuleHashes []string          `json:"AllowedModuleHashes,omitempty"`
	CodeRegions         []AntiCheatRegion `json:"CodeRegions,omitempty"`
}
type AntiCheatFile struct{ Name, Sha256 string }
type AntiCheatRule struct{ Id, Scope, Sha256 string }
type AntiCheatRegion struct {
	Id, ExeSha256 string
	Rva           int64
	Length        int
	Sha256        string
}

var antiCheatLeaf = regexp.MustCompile(`^[a-zA-Z0-9_-][a-zA-Z0-9_.-]{0,119}$`)
var antiCheatDevice = regexp.MustCompile(`(?i)^(CON|PRN|AUX|NUL|COM[0-9]|LPT[0-9])$`)

func (p *AntiCheatPolicy) clone() *AntiCheatPolicy {
	if p == nil {
		return nil
	}
	copy := *p
	copy.RequiredFiles = slices.Clone(p.RequiredFiles)
	copy.Rules = slices.Clone(p.Rules)
	copy.AllowedModuleHashes = slices.Clone(p.AllowedModuleHashes)
	copy.CodeRegions = slices.Clone(p.CodeRegions)
	return &copy
}

func (p *AntiCheatPolicy) validate() error {
	if p.Mode == "" {
		p.Mode = "audit"
	}
	invalid := errors.New("invalid anti-cheat policy")
	if p.Mode != "audit" && p.Mode != "enforce" || len(p.RequiredFiles) > 64 || len(p.Rules) > 256 || len(p.AllowedModuleHashes) > 512 || len(p.CodeRegions) > 32 {
		return invalid
	}
	paths, ids := map[string]bool{}, map[string]bool{}
	for _, f := range p.RequiredFiles {
		key := strings.ToLower(f.Name)
		if !antiCheatLeaf.MatchString(f.Name) || !strings.HasSuffix(key, ".dll") || antiCheatDevice.MatchString(strings.Split(f.Name, ".")[0]) || !digestPattern.MatchString(f.Sha256) || paths[key] {
			return invalid
		}
		paths[key] = true
	}
	for _, r := range p.Rules {
		if !idPattern.MatchString(r.Id) || ids[r.Id] || !digestPattern.MatchString(r.Sha256) || r.Scope != "module" && r.Scope != "process" {
			return invalid
		}
		ids[r.Id] = true
	}
	for _, h := range p.AllowedModuleHashes {
		if !digestPattern.MatchString(h) {
			return invalid
		}
	}
	for _, r := range p.CodeRegions {
		if !idPattern.MatchString(r.Id) || ids[r.Id] || !digestPattern.MatchString(r.ExeSha256) || !digestPattern.MatchString(r.Sha256) || r.Rva < 0 || r.Rva > 0x7fffffff || r.Length < 1 || r.Length > 32768 || r.Rva+int64(r.Length) > 0x80000000 {
			return invalid
		}
		ids[r.Id] = true
	}
	return nil
}
func bundleFlavor(file *os.File, size int64) (string, error) {
	z, err := zip.NewReader(file, size)
	if err != nil {
		return "", err
	}
	for _, f := range z.File {
		if !strings.EqualFold(f.Name, "build-flavor.json") {
			continue
		}
		if f.Name != "build-flavor.json" || f.UncompressedSize64 > 1024 {
			return "", errors.New("invalid build flavor marker")
		}
		input, err := f.Open()
		if err != nil {
			return "", err
		}
		defer input.Close()
		raw, err := io.ReadAll(io.LimitReader(input, 1025))
		if err != nil {
			return "", err
		}
		var value struct {
			Format int    `json:"format"`
			Flavor string `json:"flavor"`
		}
		if json.Unmarshal(raw, &value) != nil || value.Format != 1 || value.Flavor != "standard" && value.Flavor != "anticheat" {
			return "", errors.New("invalid build flavor marker")
		}
		return value.Flavor, nil
	}
	return "standard", nil // Existing standard releases remain compatible.
}
