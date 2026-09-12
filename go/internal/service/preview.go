package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"2fado/internal/protocol"
)

const (
	maxSamplePaths = 10
	maxWalkLimit   = 1000
)

// PreviewImpact analyzes argv, cwd, and environment to predict blast radius,
// affected targets, and risk level without performing destructive actions.
func PreviewImpact(argv []string, cwd string, env []string) *protocol.PreviewRecord {
	if len(argv) == 0 {
		return &protocol.PreviewRecord{
			TargetCwd:  cwd,
			RiskLevel:  "low",
			RiskReason: "",
		}
	}

	cleanCwd := cwd
	if cleanCwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cleanCwd = wd
		} else {
			cleanCwd = "."
		}
	}
	cleanCwd = filepath.Clean(cleanCwd)

	resolvedBin := resolveBinary(argv[0], cleanCwd, env)
	base := filepath.Base(argv[0])

	isDestructive, isDeletion, isRecursive, rawTargets := classifyCommand(base, argv)
	if !isDestructive {
		return &protocol.PreviewRecord{
			ResolvedBinary: resolvedBin,
			TargetCwd:      cleanCwd,
			AffectedCount:  0,
			SamplePaths:    nil,
			RiskLevel:      "low",
			RiskReason:     "",
		}
	}

	if len(rawTargets) == 0 {
		rawTargets = []string{"."}
	}

	var affectedCount int
	var samplePaths []string
	seenSamples := make(map[string]bool)

	hasCritical := false
	criticalReason := ""
	hasOutside := false
	outsidePath := ""

	addSample := func(p string) {
		if len(samplePaths) < maxSamplePaths && !seenSamples[p] {
			seenSamples[p] = true
			samplePaths = append(samplePaths, p)
		}
	}

	for _, rawTarget := range rawTargets {
		var targetAbs string
		if filepath.IsAbs(rawTarget) {
			targetAbs = filepath.Clean(rawTarget)
		} else {
			targetAbs = filepath.Clean(filepath.Join(cleanCwd, rawTarget))
		}

		var targetPaths []string
		if strings.ContainsAny(rawTarget, "*?[") {
			matches, err := filepath.Glob(targetAbs)
			if err == nil && len(matches) > 0 {
				targetPaths = matches
			} else {
				targetPaths = []string{targetAbs}
			}
		} else {
			targetPaths = []string{targetAbs}
		}

		for _, p := range targetPaths {
			p = filepath.Clean(p)

			if isCrit, reason := isCriticalTarget(p); isCrit {
				hasCritical = true
				if criticalReason == "" {
					criticalReason = reason
				}
			}

			if isOutsideCwd(cleanCwd, p) {
				hasOutside = true
				if outsidePath == "" {
					outsidePath = p
				}
			}

			fi, err := os.Lstat(p)
			if err != nil {
				affectedCount++
				addSample(p)
				continue
			}

			if fi.IsDir() && isRecursive {
				_ = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
					if err != nil {
						return nil
					}
					affectedCount++
					addSample(path)
					if affectedCount >= maxWalkLimit {
						return filepath.SkipAll
					}
					return nil
				})
			} else {
				affectedCount++
				addSample(p)
			}
		}
	}

	var riskLevel string
	var riskReason string

	if hasCritical {
		riskLevel = "critical"
		riskReason = criticalReason
	} else if hasOutside {
		riskLevel = "high"
		riskReason = fmt.Sprintf("Targets path outside working directory: %s", outsidePath)
	} else if affectedCount > 10 {
		riskLevel = "high"
		riskReason = fmt.Sprintf("Affects %d files (exceeds threshold of 10)", affectedCount)
	} else if isDeletion {
		riskLevel = "medium"
		if affectedCount == 1 {
			riskReason = "Deletes file within working directory"
		} else {
			riskReason = fmt.Sprintf("Deletes %d files within working directory", affectedCount)
		}
	} else {
		riskLevel = "medium"
		riskReason = "Modifies files within working directory"
	}

	return &protocol.PreviewRecord{
		ResolvedBinary: resolvedBin,
		TargetCwd:      cleanCwd,
		AffectedCount:  affectedCount,
		SamplePaths:    samplePaths,
		RiskLevel:      riskLevel,
		RiskReason:     riskReason,
	}
}

func isCriticalTarget(p string) (bool, string) {
	clean := filepath.Clean(p)
	if clean == "/" {
		return true, "Matches root filesystem pattern: /"
	}
	if clean == "/home" {
		return true, "Targets system root directory: /home"
	}

	sysDirs := []string{
		"/etc", "/usr", "/boot", "/var", "/root", "/sys", "/proc",
		"/dev", "/bin", "/sbin", "/lib", "/lib64", "/opt", "/srv",
	}
	for _, sys := range sysDirs {
		if clean == sys || strings.HasPrefix(clean, sys+"/") {
			return true, fmt.Sprintf("Targets critical system directory: %s", clean)
		}
	}
	return false, ""
}

func isOutsideCwd(cwd, target string) bool {
	cleanCwd := filepath.Clean(cwd)
	cleanTarget := filepath.Clean(target)
	rel, err := filepath.Rel(cleanCwd, cleanTarget)
	if err != nil {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true
	}
	return false
}

func resolveBinary(bin string, cwd string, env []string) string {
	if bin == "" {
		return ""
	}
	if filepath.IsAbs(bin) {
		return filepath.Clean(bin)
	}
	if strings.Contains(bin, "/") {
		if cwd != "" {
			return filepath.Clean(filepath.Join(cwd, bin))
		}
		return filepath.Clean(bin)
	}

	var pathVal string
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			pathVal = strings.TrimPrefix(kv, "PATH=")
			break
		}
	}
	if pathVal == "" {
		pathVal = os.Getenv("PATH")
	}
	if pathVal == "" {
		pathVal = safePath
	}

	dirs := filepath.SplitList(pathVal)
	for _, dir := range dirs {
		if dir == "" {
			dir = "."
		}
		var cand string
		if filepath.IsAbs(dir) {
			cand = filepath.Join(dir, bin)
		} else if cwd != "" {
			cand = filepath.Join(cwd, dir, bin)
		} else {
			cand = filepath.Join(dir, bin)
		}
		fi, err := os.Stat(cand)
		if err == nil && !fi.IsDir() && (fi.Mode()&0111 != 0) {
			return cand
		}
	}

	if lp, err := exec.LookPath(bin); err == nil {
		if filepath.IsAbs(lp) {
			return lp
		}
		if cwd != "" {
			return filepath.Clean(filepath.Join(cwd, lp))
		}
		return lp
	}

	return "/usr/bin/" + bin
}

func classifyCommand(base string, argv []string) (isDestructive bool, isDeletion bool, isRecursive bool, targets []string) {
	if (base == "sudo" || base == "doas") && len(argv) > 1 {
		i := 1
		for i < len(argv) {
			if argv[i] == "--" {
				i++
				break
			}
			if strings.HasPrefix(argv[i], "-") {
				if argv[i] == "-u" || argv[i] == "-g" || argv[i] == "-h" || argv[i] == "-p" {
					i += 2
					continue
				}
				i++
				continue
			}
			break
		}
		if i < len(argv) {
			return classifyCommand(filepath.Base(argv[i]), argv[i:])
		}
	}

	switch base {
	case "rm", "unlink", "shred", "wipe":
		isDestructive = true
		isDeletion = true
		targets, isRecursive = extractRmTargets(argv)
		return

	case "git":
		isClean := false
		for _, a := range argv[1:] {
			if a == "clean" {
				isClean = true
				break
			}
			if !strings.HasPrefix(a, "-") {
				break
			}
		}
		if isClean {
			isDestructive = true
			isDeletion = true
			targets, isRecursive = extractGitCleanTargets(argv)
			return
		}

	case "mv":
		isDestructive = true
		isDeletion = false
		targets = extractMvTargets(argv)
		return

	case "chmod":
		isDestructive = true
		isDeletion = false
		targets, isRecursive = extractChmodTargets(argv)
		return

	case "chown", "chgrp":
		isDestructive = true
		isDeletion = false
		targets, isRecursive = extractChownTargets(argv)
		return

	case "truncate":
		isDestructive = true
		isDeletion = false
		targets = extractTruncateTargets(argv)
		return

	case "find":
		var isDel bool
		targets, isDel = extractFindTargets(argv)
		if isDel {
			isDestructive = true
			isDeletion = true
			return
		}
	}

	return false, false, false, nil
}

func extractRmTargets(argv []string) (targets []string, isRecursive bool) {
	afterDashDash := false
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if afterDashDash {
			targets = append(targets, arg)
			continue
		}
		if arg == "--" {
			afterDashDash = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if arg == "--recursive" {
				isRecursive = true
			} else if !strings.HasPrefix(arg, "--") {
				if strings.ContainsAny(arg, "rR") {
					isRecursive = true
				}
			}
			continue
		}
		targets = append(targets, arg)
	}
	return targets, isRecursive
}

func extractGitCleanTargets(argv []string) (targets []string, isRecursive bool) {
	cleanFound := false
	afterDashDash := false
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if !cleanFound {
			if arg == "clean" {
				cleanFound = true
			}
			continue
		}
		if afterDashDash {
			targets = append(targets, arg)
			continue
		}
		if arg == "--" {
			afterDashDash = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if strings.Contains(arg, "d") {
				isRecursive = true
			}
			if arg == "-e" || arg == "--exclude" {
				i++
			}
			continue
		}
		targets = append(targets, arg)
	}
	return targets, isRecursive
}

func extractMvTargets(argv []string) (targets []string) {
	afterDashDash := false
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if afterDashDash {
			targets = append(targets, arg)
			continue
		}
		if arg == "--" {
			afterDashDash = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if arg == "-t" || arg == "--target-directory" || arg == "-S" || arg == "--suffix" {
				i++
			}
			continue
		}
		targets = append(targets, arg)
	}
	return targets
}

func extractChmodTargets(argv []string) (targets []string, isRecursive bool) {
	afterDashDash := false
	seenMode := false
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if afterDashDash {
			if !seenMode {
				seenMode = true
				continue
			}
			targets = append(targets, arg)
			continue
		}
		if arg == "--" {
			afterDashDash = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if arg == "--recursive" || strings.Contains(arg, "R") {
				isRecursive = true
			}
			if arg == "--reference" {
				i++
			}
			continue
		}
		if !seenMode {
			seenMode = true
			continue
		}
		targets = append(targets, arg)
	}
	return targets, isRecursive
}

func extractChownTargets(argv []string) (targets []string, isRecursive bool) {
	afterDashDash := false
	seenOwner := false
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if afterDashDash {
			if !seenOwner {
				seenOwner = true
				continue
			}
			targets = append(targets, arg)
			continue
		}
		if arg == "--" {
			afterDashDash = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if arg == "--recursive" || strings.Contains(arg, "R") {
				isRecursive = true
			}
			if arg == "--reference" {
				i++
			}
			continue
		}
		if !seenOwner {
			seenOwner = true
			continue
		}
		targets = append(targets, arg)
	}
	return targets, isRecursive
}

func extractTruncateTargets(argv []string) (targets []string) {
	afterDashDash := false
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if afterDashDash {
			targets = append(targets, arg)
			continue
		}
		if arg == "--" {
			afterDashDash = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if arg == "-s" || arg == "--size" || arg == "-r" || arg == "--reference" {
				i++
			}
			continue
		}
		targets = append(targets, arg)
	}
	return targets
}

func extractFindTargets(argv []string) (targets []string, isDeletion bool) {
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if arg == "-delete" {
			isDeletion = true
		}
		if arg == "-exec" || arg == "-execdir" {
			for j := i + 1; j < len(argv); j++ {
				if argv[j] == "rm" {
					isDeletion = true
				}
			}
		}
		if strings.HasPrefix(arg, "-") || arg == "!" || arg == "(" || arg == ")" {
			break
		}
		targets = append(targets, arg)
	}
	if len(targets) == 0 {
		targets = []string{"."}
	}
	return targets, isDeletion
}
