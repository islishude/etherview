// Package plancheck validates Etherview's repository-local planning contract.
package plancheck

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	planIDPattern     = regexp.MustCompile(`^P[0-9]{2}$`)
	workItemIDPattern = regexp.MustCompile(`^P[0-9]{2}-T[0-9]{2}$`)
	dependencyPattern = regexp.MustCompile(`P[0-9]{2}(?:-T[0-9]{2})?`)
	dependencyRange   = regexp.MustCompile(`(P[0-9]{2}(?:-T[0-9]{2})?)\s*[-\x{2013}\x{2014}]\s*(P[0-9]{2}(?:-T[0-9]{2})?)`)
	dependencyToken   = regexp.MustCompile(`P[0-9][0-9A-Za-z-]*`)
	markdownLink      = regexp.MustCompile(`!?\[[^]]*\]\(([^)]+)\)`)
	checkboxPattern   = regexp.MustCompile(`^\s*-\s*\[([ xX])\]\s+(.+)$`)
)

var allowedPlanStatuses = map[string]bool{
	"planned":     true,
	"in_progress": true,
	"blocked":     true,
	"done":        true,
	"superseded":  true,
}

var allowedItemStatuses = map[string]bool{
	"todo":        true,
	"in_progress": true,
	"blocked":     true,
	"done":        true,
	"dropped":     true,
}

// Diagnostic describes one actionable plan validation error.
type Diagnostic struct {
	Path    string
	Line    int
	Message string
}

// String returns a compiler-style, repository-relative diagnostic.
func (d Diagnostic) String() string {
	if d.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", d.Path, d.Line, d.Message)
	}
	return fmt.Sprintf("%s: %s", d.Path, d.Message)
}

// Report is the deterministic result of checking a repository.
type Report struct {
	Diagnostics []Diagnostic
	Plans       int
	WorkItems   int
	Links       int
}

// OK reports whether the planning contract is valid.
func (r Report) OK() bool { return len(r.Diagnostics) == 0 }

type document struct {
	path  string
	lines []string
}

type rootPlan struct {
	id            string
	status        string
	dependsOn     string
	link          string
	line          int
	dependencyIDs []string
}

type childPlan struct {
	id         string
	status     string
	path       string
	statusLine int
	items      []*workItem
	acceptance []checklistItem
	evidence   []numberedLine
	blockers   []numberedLine
	root       *rootPlan
}

type workItem struct {
	id            string
	status        string
	dependsOn     string
	deliverable   string
	verification  string
	line          int
	plan          *childPlan
	dependencyIDs []string
}

type checklistItem struct {
	checked bool
	line    int
	text    string
}

type numberedLine struct {
	number int
	text   string
}

type checker struct {
	root        string
	diagnostics []Diagnostic
	links       int
	rootDoc     document
	rootPlans   []*rootPlan
	children    []*childPlan
	plansByID   map[string]*childPlan
	itemsByID   map[string]*workItem
}

// Check validates PLAN.md and docs/plans beneath root. It reports malformed or
// inconsistent content as diagnostics instead of stopping at the first error.
func Check(root string) Report {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Report{Diagnostics: []Diagnostic{{Path: "PLAN.md", Message: fmt.Sprintf("resolve repository root: %v", err)}}}
	}

	c := &checker{
		root:      absRoot,
		plansByID: make(map[string]*childPlan),
		itemsByID: make(map[string]*workItem),
	}
	c.run()
	sort.Slice(c.diagnostics, func(i, j int) bool {
		left, right := c.diagnostics[i], c.diagnostics[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		return left.Message < right.Message
	})

	return Report{
		Diagnostics: c.diagnostics,
		Plans:       len(c.children),
		WorkItems:   len(c.itemsByID),
		Links:       c.links,
	}
}

func (c *checker) run() {
	rootDoc, ok := c.readDocument("PLAN.md")
	if !ok {
		return
	}
	c.rootDoc = rootDoc
	c.checkLinks(rootDoc)
	c.rootPlans = c.parseRootPlans(rootDoc)

	matches, err := filepath.Glob(filepath.Join(c.root, "docs", "plans", "*.md"))
	if err != nil {
		c.add("docs/plans", 0, fmt.Sprintf("enumerate child plans: %v", err))
		return
	}
	if len(matches) == 0 {
		c.add("docs/plans", 0, "no child plan documents found")
		return
	}
	sort.Strings(matches)
	for _, match := range matches {
		rel, err := filepath.Rel(c.root, match)
		if err != nil {
			c.add("docs/plans", 0, fmt.Sprintf("resolve child plan path: %v", err))
			continue
		}
		rel = filepath.ToSlash(rel)
		doc, ok := c.readDocument(rel)
		if !ok {
			continue
		}
		c.checkLinks(doc)
		if filepath.Base(match) == "_template.md" {
			continue
		}
		if !strings.HasPrefix(filepath.Base(match), "P") {
			continue
		}
		plan := c.parseChildPlan(doc)
		if plan != nil {
			c.children = append(c.children, plan)
		}
	}

	c.indexAndCrossCheckPlans()
	c.indexWorkItems()
	c.validateDependencies()
	c.validateStateAndEvidence()
	c.detectDependencyCycles()
}

func (c *checker) readDocument(rel string) (document, bool) {
	data, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(rel)))
	if err != nil {
		c.add(rel, 0, fmt.Sprintf("read document: %v", err))
		return document{}, false
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	return document{path: rel, lines: strings.Split(text, "\n")}, true
}

func (c *checker) parseRootPlans(doc document) []*rootPlan {
	table, ok := findTable(doc, []string{"ID", "Plan", "Status", "Depends on", "Outcome"})
	if !ok {
		c.add(doc.path, 0, "missing Plan Index table with ID, Plan, Status, Depends on, and Outcome columns")
		return nil
	}

	var plans []*rootPlan
	seen := make(map[string]int)
	for _, row := range table.rows {
		if len(row.cells) != table.width {
			c.add(doc.path, row.line, fmt.Sprintf("Plan Index row has %d cells, expected %d", len(row.cells), table.width))
		}
		id := cleanCell(row.cell(table, "ID"))
		status := cleanCell(row.cell(table, "Status"))
		plan := &rootPlan{
			id:        id,
			status:    status,
			dependsOn: cleanCell(row.cell(table, "Depends on")),
			link:      firstLinkTarget(row.cell(table, "Plan")),
			line:      row.line,
		}
		plans = append(plans, plan)
		if !planIDPattern.MatchString(id) {
			c.add(doc.path, row.line, fmt.Sprintf("malformed plan ID %q; expected P followed by two digits", id))
		}
		if previous, duplicate := seen[id]; duplicate {
			c.add(doc.path, row.line, fmt.Sprintf("duplicate plan ID %s (first declared on line %d)", id, previous))
		} else {
			seen[id] = row.line
		}
		if !allowedPlanStatuses[status] {
			c.add(doc.path, row.line, fmt.Sprintf("plan %s has unsupported status %q", id, status))
		}
		if plan.link == "" {
			c.add(doc.path, row.line, fmt.Sprintf("plan %s must link to its child plan document", id))
		}
	}
	return plans
}

func (c *checker) parseChildPlan(doc document) *childPlan {
	plan := &childPlan{path: doc.path}
	headingLine := 0
	for i, line := range doc.lines {
		if !strings.HasPrefix(line, "# ") {
			continue
		}
		headingLine = i + 1
		heading := strings.TrimSpace(strings.TrimPrefix(line, "# "))
		fields := strings.Fields(heading)
		if len(fields) > 0 {
			plan.id = fields[0]
		}
		break
	}
	if plan.id == "" {
		c.add(doc.path, 0, "missing level-one heading with plan ID")
		return nil
	}
	if !planIDPattern.MatchString(plan.id) {
		c.add(doc.path, headingLine, fmt.Sprintf("malformed child plan ID %q; expected P followed by two digits", plan.id))
	}
	wantPrefix := plan.id + "-"
	if !strings.HasPrefix(filepath.Base(doc.path), wantPrefix) {
		c.add(doc.path, headingLine, fmt.Sprintf("filename must start with %s", wantPrefix))
	}

	for i, line := range doc.lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "Status:") {
			continue
		}
		plan.status = cleanCell(strings.TrimSpace(strings.TrimPrefix(trimmed, "Status:")))
		plan.statusLine = i + 1
		break
	}
	if plan.status == "" {
		c.add(doc.path, 0, fmt.Sprintf("plan %s is missing a Status declaration", plan.id))
	} else if !allowedPlanStatuses[plan.status] {
		c.add(doc.path, plan.statusLine, fmt.Sprintf("plan %s has unsupported status %q", plan.id, plan.status))
	}

	table, ok := findTable(doc, []string{"ID", "Status", "Depends on", "Deliverable", "Verification"})
	if !ok {
		c.add(doc.path, 0, "missing Work Items table with ID, Status, Depends on, Deliverable, and Verification columns")
	} else {
		for _, row := range table.rows {
			if len(row.cells) != table.width {
				c.add(doc.path, row.line, fmt.Sprintf("Work Items row has %d cells, expected %d", len(row.cells), table.width))
			}
			plan.items = append(plan.items, &workItem{
				id:           cleanCell(row.cell(table, "ID")),
				status:       cleanCell(row.cell(table, "Status")),
				dependsOn:    cleanCell(row.cell(table, "Depends on")),
				deliverable:  cleanCell(row.cell(table, "Deliverable")),
				verification: cleanCell(row.cell(table, "Verification")),
				line:         row.line,
				plan:         plan,
			})
		}
	}

	plan.acceptance = parseChecklist(sectionLines(doc, "Acceptance"))
	plan.evidence = sectionLines(doc, "Evidence")
	plan.blockers = sectionLines(doc, "Current Blockers")
	if len(plan.acceptance) == 0 {
		c.add(doc.path, 0, fmt.Sprintf("plan %s must contain at least one Acceptance checklist item", plan.id))
	}
	return plan
}

func (c *checker) indexAndCrossCheckPlans() {
	rootByID := make(map[string]*rootPlan)
	linkedChildren := make(map[string]bool)
	for _, plan := range c.rootPlans {
		if _, exists := rootByID[plan.id]; !exists {
			rootByID[plan.id] = plan
		}

		resolved, ok := c.resolveLocalTarget(c.rootDoc.path, plan.link)
		if ok {
			linkedChildren[resolved] = true
		}
	}

	for _, child := range c.children {
		if previous, duplicate := c.plansByID[child.id]; duplicate {
			c.add(child.path, 1, fmt.Sprintf("duplicate child plan ID %s (also declared in %s)", child.id, previous.path))
			continue
		}
		c.plansByID[child.id] = child
		root := rootByID[child.id]
		if root == nil {
			c.add(child.path, 1, fmt.Sprintf("child plan %s is not listed in PLAN.md", child.id))
			continue
		}
		child.root = root
		resolved, ok := c.resolveLocalTarget(c.rootDoc.path, root.link)
		if ok && resolved != child.path {
			c.add(c.rootDoc.path, root.line, fmt.Sprintf("plan %s link resolves to %s, expected %s", child.id, resolved, child.path))
		}
		if root.status != child.status {
			c.add(c.rootDoc.path, root.line, fmt.Sprintf("plan %s status %q does not match child status %q", child.id, root.status, child.status))
		}
	}

	for _, root := range c.rootPlans {
		if c.plansByID[root.id] == nil {
			c.add(c.rootDoc.path, root.line, fmt.Sprintf("plan %s has no child plan document", root.id))
		}
	}
	for _, child := range c.children {
		if !linkedChildren[child.path] {
			c.add(child.path, 1, fmt.Sprintf("child plan %s is not linked from PLAN.md", child.id))
		}
	}
}

func (c *checker) indexWorkItems() {
	for _, plan := range c.children {
		localSeen := make(map[string]int)
		for _, item := range plan.items {
			if !workItemIDPattern.MatchString(item.id) {
				c.add(plan.path, item.line, fmt.Sprintf("malformed work-item ID %q; expected PNN-TNN", item.id))
			} else if !strings.HasPrefix(item.id, plan.id+"-T") {
				c.add(plan.path, item.line, fmt.Sprintf("work item %s must use parent prefix %s-T", item.id, plan.id))
			}
			if previous, duplicate := localSeen[item.id]; duplicate {
				c.add(plan.path, item.line, fmt.Sprintf("duplicate work-item ID %s (first declared on line %d)", item.id, previous))
			} else {
				localSeen[item.id] = item.line
			}
			if previous, duplicate := c.itemsByID[item.id]; duplicate {
				c.add(plan.path, item.line, fmt.Sprintf("duplicate work-item ID %s (also declared in %s:%d)", item.id, previous.plan.path, previous.line))
			} else {
				c.itemsByID[item.id] = item
			}
			if !allowedItemStatuses[item.status] {
				c.add(plan.path, item.line, fmt.Sprintf("work item %s has unsupported status %q", item.id, item.status))
			}
			if isPlaceholder(item.deliverable) {
				c.add(plan.path, item.line, fmt.Sprintf("work item %s must have a concrete deliverable", item.id))
			}
			if isPlaceholder(item.verification) {
				c.add(plan.path, item.line, fmt.Sprintf("work item %s must have a concrete verification", item.id))
			}
		}
	}
}

func (c *checker) validateDependencies() {
	rootIDs := make(map[string]bool)
	for _, plan := range c.rootPlans {
		if planIDPattern.MatchString(plan.id) {
			rootIDs[plan.id] = true
		}
	}
	for _, plan := range c.rootPlans {
		plan.dependencyIDs = c.parseDependencies(c.rootDoc.path, plan.line, plan.id, plan.dependsOn, false)
		for _, dependency := range plan.dependencyIDs {
			if !planIDPattern.MatchString(dependency) {
				c.add(c.rootDoc.path, plan.line, fmt.Sprintf("plan %s dependency %s must reference a plan ID", plan.id, dependency))
				continue
			}
			if !rootIDs[dependency] {
				c.add(c.rootDoc.path, plan.line, fmt.Sprintf("plan %s dependency %s does not resolve", plan.id, dependency))
			}
			if dependency == plan.id {
				c.add(c.rootDoc.path, plan.line, fmt.Sprintf("plan %s depends on itself", plan.id))
			}
		}
	}

	for _, plan := range c.children {
		for _, item := range plan.items {
			item.dependencyIDs = c.parseDependencies(plan.path, item.line, item.id, item.dependsOn, true)
			for _, dependency := range item.dependencyIDs {
				switch {
				case planIDPattern.MatchString(dependency):
					dependencyPlan := c.plansByID[dependency]
					if dependencyPlan == nil {
						c.add(plan.path, item.line, fmt.Sprintf("work item %s dependency %s does not resolve", item.id, dependency))
					} else if dependency == plan.id {
						c.add(plan.path, item.line, fmt.Sprintf("work item %s cannot depend on its containing plan %s", item.id, dependency))
					} else if isActiveItem(item.status) && dependencyPlan.status != "done" {
						c.add(plan.path, item.line, fmt.Sprintf("work item %s is %s but dependency plan %s is %s", item.id, item.status, dependency, dependencyPlan.status))
					}
				case workItemIDPattern.MatchString(dependency):
					dependencyItem := c.itemsByID[dependency]
					if dependencyItem == nil {
						c.add(plan.path, item.line, fmt.Sprintf("work item %s dependency %s does not resolve", item.id, dependency))
					} else if dependency == item.id {
						c.add(plan.path, item.line, fmt.Sprintf("work item %s depends on itself", item.id))
					} else if isActiveItem(item.status) && dependencyItem.status != "done" {
						c.add(plan.path, item.line, fmt.Sprintf("work item %s is %s but dependency %s is %s", item.id, item.status, dependency, dependencyItem.status))
					}
				default:
					c.add(plan.path, item.line, fmt.Sprintf("work item %s has malformed dependency %q", item.id, dependency))
				}
			}
		}
	}
}

func (c *checker) parseDependencies(path string, line int, owner, value string, allowItems bool) []string {
	trimmed := strings.TrimSpace(value)
	if isNoDependency(trimmed) {
		return nil
	}

	var dependencies []string
	withoutRanges := dependencyRange.ReplaceAllStringFunc(trimmed, func(match string) string {
		parts := dependencyRange.FindStringSubmatch(match)
		if len(parts) != 3 {
			return " "
		}
		dependencies = append(dependencies, c.expandRange(path, line, owner, parts[1], parts[2], allowItems)...)
		return " "
	})
	for _, token := range dependencyToken.FindAllString(withoutRanges, -1) {
		if !planIDPattern.MatchString(token) && !workItemIDPattern.MatchString(token) {
			c.add(path, line, fmt.Sprintf("%s has malformed dependency ID %q", owner, token))
		}
	}
	dependencies = append(dependencies, dependencyPattern.FindAllString(withoutRanges, -1)...)
	if len(dependencies) == 0 {
		c.add(path, line, fmt.Sprintf("%s dependency cell %q contains no valid IDs", owner, value))
		return nil
	}

	seen := make(map[string]bool)
	unique := dependencies[:0]
	for _, dependency := range dependencies {
		if seen[dependency] {
			continue
		}
		seen[dependency] = true
		unique = append(unique, dependency)
	}
	return unique
}

func (c *checker) expandRange(path string, line int, owner, first, last string, allowItems bool) []string {
	firstIsItem := workItemIDPattern.MatchString(first)
	lastIsItem := workItemIDPattern.MatchString(last)
	if firstIsItem != lastIsItem {
		c.add(path, line, fmt.Sprintf("%s dependency range %s–%s mixes plan and work-item IDs", owner, first, last))
		return []string{first, last}
	}
	if firstIsItem {
		if !allowItems {
			c.add(path, line, fmt.Sprintf("%s dependency range %s–%s cannot contain work-item IDs", owner, first, last))
			return []string{first, last}
		}
		firstPlan, lastPlan := first[:3], last[:3]
		if firstPlan != lastPlan {
			c.add(path, line, fmt.Sprintf("%s work-item dependency range %s–%s must stay within one plan", owner, first, last))
			return []string{first, last}
		}
		start, _ := strconv.Atoi(first[5:])
		end, _ := strconv.Atoi(last[5:])
		if start > end {
			c.add(path, line, fmt.Sprintf("%s dependency range %s–%s is reversed", owner, first, last))
			return []string{first, last}
		}
		result := make([]string, 0, end-start+1)
		for value := start; value <= end; value++ {
			result = append(result, fmt.Sprintf("%s-T%02d", firstPlan, value))
		}
		return result
	}

	start, _ := strconv.Atoi(first[1:])
	end, _ := strconv.Atoi(last[1:])
	if start > end {
		c.add(path, line, fmt.Sprintf("%s dependency range %s–%s is reversed", owner, first, last))
		return []string{first, last}
	}
	result := []string{first}
	for _, plan := range c.rootPlans {
		if !planIDPattern.MatchString(plan.id) {
			continue
		}
		value, _ := strconv.Atoi(plan.id[1:])
		if value >= start && value <= end {
			result = append(result, plan.id)
		}
	}
	result = append(result, last)
	return result
}

func (c *checker) validateStateAndEvidence() {
	for _, plan := range c.children {
		started := false
		blocked := false
		allFinished := len(plan.items) > 0
		for _, item := range plan.items {
			switch item.status {
			case "in_progress", "done":
				started = true
			case "blocked":
				blocked = true
			}
			if item.status != "done" && item.status != "dropped" {
				allFinished = false
			}
			if item.status == "done" && !sectionMentions(plan.evidence, item.id) {
				c.add(plan.path, item.line, fmt.Sprintf("done work item %s must have non-placeholder evidence that mentions its ID", item.id))
			}
			if item.status == "dropped" && !sectionMentions(plan.evidence, item.id) {
				c.add(plan.path, item.line, fmt.Sprintf("dropped work item %s must have evidence that mentions its ID and reason", item.id))
			}
			if item.status == "blocked" && !sectionMentions(plan.blockers, item.id) {
				c.add(plan.path, item.line, fmt.Sprintf("blocked work item %s must be described in Current Blockers", item.id))
			}
		}

		switch plan.status {
		case "planned":
			if started || blocked {
				c.add(plan.path, plan.statusLine, fmt.Sprintf("planned plan %s contains started or blocked work items", plan.id))
			}
		case "in_progress":
			if !started && !blocked {
				c.add(plan.path, plan.statusLine, fmt.Sprintf("in_progress plan %s has no started work item", plan.id))
			}
		case "blocked":
			if !blocked {
				c.add(plan.path, plan.statusLine, fmt.Sprintf("blocked plan %s has no blocked work item", plan.id))
			}
		case "done":
			if !allFinished {
				c.add(plan.path, plan.statusLine, fmt.Sprintf("done plan %s still has unfinished work items", plan.id))
			}
			for _, acceptance := range plan.acceptance {
				if !acceptance.checked {
					c.add(plan.path, acceptance.line, fmt.Sprintf("done plan %s has unchecked acceptance criterion", plan.id))
				}
			}
			if !hasMeaningfulSection(plan.evidence) {
				c.add(plan.path, plan.statusLine, fmt.Sprintf("done plan %s must contain non-placeholder evidence", plan.id))
			}
		case "superseded":
			if !hasMeaningfulSection(plan.evidence) {
				c.add(plan.path, plan.statusLine, fmt.Sprintf("superseded plan %s must identify its replacement in Evidence", plan.id))
			}
		}
	}

	if release := c.plansByID["P70"]; release != nil && release.status == "done" {
		for _, gate := range parseChecklist(sectionLines(c.rootDoc, "Global Release Gates")) {
			if !gate.checked {
				c.add(c.rootDoc.path, gate.line, "P70 is done but a Global Release Gate is unchecked")
			}
		}
	}
}

func (c *checker) detectDependencyCycles() {
	planGraph := make(map[string][]string)
	for _, plan := range c.rootPlans {
		for _, dependency := range plan.dependencyIDs {
			if planIDPattern.MatchString(dependency) && c.plansByID[dependency] != nil {
				planGraph[plan.id] = append(planGraph[plan.id], dependency)
			}
		}
	}
	c.detectCycles(planGraph, func(id string) (string, int) {
		for _, plan := range c.rootPlans {
			if plan.id == id {
				return c.rootDoc.path, plan.line
			}
		}
		return c.rootDoc.path, 0
	}, "plan dependency cycle")

	itemGraph := make(map[string][]string)
	for _, item := range c.itemsByID {
		for _, dependency := range item.dependencyIDs {
			if workItemIDPattern.MatchString(dependency) && c.itemsByID[dependency] != nil {
				itemGraph[item.id] = append(itemGraph[item.id], dependency)
			}
		}
	}
	c.detectCycles(itemGraph, func(id string) (string, int) {
		item := c.itemsByID[id]
		if item == nil {
			return "PLAN.md", 0
		}
		return item.plan.path, item.line
	}, "work-item dependency cycle")
}

func (c *checker) detectCycles(graph map[string][]string, location func(string) (string, int), label string) {
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int)
	stack := make([]string, 0)
	reported := make(map[string]bool)
	var visit func(string)
	visit = func(node string) {
		state[node] = visiting
		stack = append(stack, node)
		for _, next := range graph[node] {
			switch state[next] {
			case unvisited:
				visit(next)
			case visiting:
				start := 0
				for i, value := range stack {
					if value == next {
						start = i
						break
					}
				}
				cycle := append(append([]string(nil), stack[start:]...), next)
				key := strings.Join(cycle, " -> ")
				if !reported[key] {
					path, line := location(node)
					c.add(path, line, fmt.Sprintf("%s: %s", label, key))
					reported[key] = true
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[node] = visited
	}

	keys := make([]string, 0, len(graph))
	for key := range graph {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if state[key] == unvisited {
			visit(key)
		}
	}
}

func (c *checker) checkLinks(doc document) {
	for index, line := range doc.lines {
		for _, match := range markdownLink.FindAllStringSubmatch(line, -1) {
			if len(match) != 2 {
				continue
			}
			target := parseLinkTarget(match[1])
			if target == "" || isExternalTarget(target) {
				continue
			}
			c.links++
			resolved, ok := c.resolveLocalTarget(doc.path, target)
			if !ok {
				c.add(doc.path, index+1, fmt.Sprintf("local link %q escapes the repository", target))
				continue
			}
			if _, err := os.Stat(filepath.Join(c.root, filepath.FromSlash(resolved))); err != nil {
				if os.IsNotExist(err) {
					c.add(doc.path, index+1, fmt.Sprintf("local link target %q does not exist", target))
				} else {
					c.add(doc.path, index+1, fmt.Sprintf("inspect local link target %q: %v", target, err))
				}
			}
		}
	}
}

func (c *checker) resolveLocalTarget(source, rawTarget string) (string, bool) {
	target := parseLinkTarget(rawTarget)
	if target == "" || isExternalTarget(target) {
		return "", false
	}
	if fragment := strings.IndexByte(target, '#'); fragment >= 0 {
		target = target[:fragment]
	}
	if query := strings.IndexByte(target, '?'); query >= 0 {
		target = target[:query]
	}
	decoded, err := url.PathUnescape(target)
	if err == nil {
		target = decoded
	}
	if target == "" {
		return source, true
	}
	abs := filepath.Clean(filepath.Join(c.root, filepath.Dir(filepath.FromSlash(source)), filepath.FromSlash(target)))
	rel, err := filepath.Rel(c.root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func (c *checker) add(path string, line int, message string) {
	c.diagnostics = append(c.diagnostics, Diagnostic{Path: filepath.ToSlash(path), Line: line, Message: message})
}

type markdownTable struct {
	headings map[string]int
	width    int
	rows     []tableRow
}

type tableRow struct {
	cells []string
	line  int
}

func (row tableRow) cell(table markdownTable, heading string) string {
	position, ok := table.headings[strings.ToLower(heading)]
	if !ok || position >= len(row.cells) {
		return ""
	}
	return row.cells[position]
}

func findTable(doc document, required []string) (markdownTable, bool) {
	for index, line := range doc.lines {
		headers, ok := splitTableRow(line)
		if !ok {
			continue
		}
		positions := make(map[string]int)
		for position, header := range headers {
			positions[strings.ToLower(cleanCell(header))] = position
		}
		complete := true
		for _, heading := range required {
			if _, exists := positions[strings.ToLower(heading)]; !exists {
				complete = false
				break
			}
		}
		if !complete || index+1 >= len(doc.lines) {
			continue
		}
		separator, ok := splitTableRow(doc.lines[index+1])
		if !ok || len(separator) != len(headers) || !isTableSeparator(separator) {
			continue
		}
		table := markdownTable{headings: positions, width: len(headers)}
		for rowIndex := index + 2; rowIndex < len(doc.lines); rowIndex++ {
			cells, ok := splitTableRow(doc.lines[rowIndex])
			if !ok {
				break
			}
			table.rows = append(table.rows, tableRow{cells: cells, line: rowIndex + 1})
		}
		return table, true
	}
	return markdownTable{}, false
}

func splitTableRow(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 || trimmed[0] != '|' || trimmed[len(trimmed)-1] != '|' {
		return nil, false
	}
	trimmed = trimmed[1 : len(trimmed)-1]
	var cells []string
	var current strings.Builder
	escaped := false
	for _, character := range trimmed {
		switch {
		case escaped:
			current.WriteRune(character)
			escaped = false
		case character == '\\':
			current.WriteRune(character)
			escaped = true
		case character == '|':
			cells = append(cells, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(character)
		}
	}
	cells = append(cells, strings.TrimSpace(current.String()))
	return cells, true
}

func isTableSeparator(cells []string) bool {
	for _, cell := range cells {
		trimmed := strings.Trim(strings.TrimSpace(cell), ":")
		if len(trimmed) < 3 || strings.Trim(trimmed, "-") != "" {
			return false
		}
	}
	return true
}

func sectionLines(doc document, title string) []numberedLine {
	heading := "## " + title
	start := -1
	for index, line := range doc.lines {
		if strings.EqualFold(strings.TrimSpace(line), heading) {
			start = index + 1
			break
		}
	}
	if start < 0 {
		return nil
	}
	var result []numberedLine
	for index := start; index < len(doc.lines); index++ {
		line := doc.lines[index]
		if strings.HasPrefix(strings.TrimSpace(line), "## ") {
			break
		}
		result = append(result, numberedLine{number: index + 1, text: line})
	}
	return result
}

func parseChecklist(lines []numberedLine) []checklistItem {
	var items []checklistItem
	for _, line := range lines {
		match := checkboxPattern.FindStringSubmatch(line.text)
		if len(match) != 3 {
			continue
		}
		items = append(items, checklistItem{
			checked: strings.EqualFold(match[1], "x"),
			line:    line.number,
			text:    strings.TrimSpace(match[2]),
		})
	}
	return items
}

func parseLinkTarget(raw string) string {
	target := strings.TrimSpace(raw)
	if strings.HasPrefix(target, "<") {
		if end := strings.Index(target, ">"); end >= 0 {
			return target[1:end]
		}
	}
	if separator := strings.IndexAny(target, " \t"); separator >= 0 {
		target = target[:separator]
	}
	return strings.TrimSpace(target)
}

func firstLinkTarget(cell string) string {
	match := markdownLink.FindStringSubmatch(cell)
	if len(match) != 2 {
		return ""
	}
	return parseLinkTarget(match[1])
}

func cleanCell(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '`' && value[len(value)-1] == '`' {
		value = value[1 : len(value)-1]
	}
	return strings.TrimSpace(value)
}

func isExternalTarget(target string) bool {
	if strings.HasPrefix(target, "#") {
		return false
	}
	parsed, err := url.Parse(target)
	return err == nil && (parsed.IsAbs() || parsed.Host != "")
}

func isNoDependency(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "-", "—", "–", "none", "n/a":
		return true
	default:
		return false
	}
}

func isPlaceholder(value string) bool {
	trimmed := strings.ToLower(strings.Trim(strings.TrimSpace(value), ".`"))
	switch trimmed {
	case "", "-", "—", "–", "none", "none yet", "pending", "tbd", "todo", "n/a":
		return true
	default:
		return strings.HasPrefix(trimmed, "add concise")
	}
}

func hasMeaningfulSection(lines []numberedLine) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line.text)
		if trimmed == "" || isPlaceholder(strings.TrimPrefix(trimmed, "- ")) {
			continue
		}
		return true
	}
	return false
}

func sectionMentions(lines []numberedLine, id string) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line.text), "- "))
		if isPlaceholder(trimmed) {
			continue
		}
		for _, token := range strings.FieldsFunc(trimmed, func(r rune) bool {
			return !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-'
		}) {
			if token == id {
				return true
			}
		}
	}
	return false
}

func isActiveItem(status string) bool {
	return status == "in_progress" || status == "done"
}
