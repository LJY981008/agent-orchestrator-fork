package ladologwatch

import (
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// CIMaxBytes caps the .gitlab-ci.yml file ParseGitLabCI reads.
	CIMaxBytes = 256 << 10
	// ciDefaultPort is the port of a host given without one: the plain
	// Docker Engine API port the deploy jobs use.
	ciDefaultPort = "2375"
	// ciMaxExpand bounds variable expansion (variables that name others).
	ciMaxExpand = 8
	// ciMaxExpandGrowth bounds how much expansion may lengthen a value: a
	// variable that names itself several times would otherwise double on
	// every pass and exhaust the daemon's memory.
	ciMaxExpandGrowth = 4 << 10
	// ciMaxDepth bounds the walk through nested nodes and aliases.
	ciMaxDepth = 64
	// ciMaxVisits bounds the nodes one parse visits. yaml.v3 does not
	// expand aliases into a yaml.Node, so its alias ratio guard does not
	// apply here; aliases of aliases ("b: &b [*a, *a, ...]") or nested
	// "<<: [*m, *m, ...]" merge keys would otherwise make a small file
	// visit about 10^9 nodes. A real .gitlab-ci.yml stays far below it.
	ciMaxVisits = 200_000
	// ciMaxScanBytes bounds the script text one parse searches, for a long
	// string that aliases repeat.
	ciMaxScanBytes = 8 << 20
)

// CICandidates are what a .gitlab-ci.yml suggests for the log watch: Docker
// hosts and compose projects. They are candidates the user checks and saves;
// nothing is saved from them.
type CICandidates struct {
	Hosts           []CIHost
	ComposeProjects []string
	// Notes say what the file had that was not read (include:, a YAML
	// error, the size cap).
	Notes []string
}

// CIHost is one Docker host candidate.
type CIHost struct {
	// Variable is the CI variable the host came from ("PROD1_HOST"), or
	// "DOCKER_HOST" for a host written into DOCKER_HOST itself.
	Variable string
	// DisplayName is a name for the source: the variable's name without
	// "_HOST", in lower case.
	DisplayName string
	// Endpoint is "tcp://host:port". When Unresolved is set it still holds
	// the variable reference ("tcp://$PROD_HOST:2375") for the user to fill.
	Endpoint string
	// Unresolved is the first variable reference the file does not define
	// ("$PROD_HOST"), usually a CI/CD variable kept in GitLab's settings.
	Unresolved string
}

// ciKeywords are the top-level keys of .gitlab-ci.yml that are not jobs.
var ciKeywords = map[string]bool{
	"default": true, "include": true, "stages": true, "variables": true, "workflow": true,
	"image": true, "services": true, "cache": true, "before_script": true, "after_script": true,
	"spec": true,
}

var (
	ciDockerHost = regexp.MustCompile(`DOCKER_HOST["']?[ \t]*[=:][ \t]*["']?(tcp://[^\s"';|&)]+)`)
	ciCompose    = regexp.MustCompile(`docker(?:-compose|[ \t]+compose)\b[^\n;&|]*?[ \t](?:-p|--project-name)(?:[ \t]+|=)["']?([^\s"';&|]+)`)
	ciComposeEnv = regexp.MustCompile(`COMPOSE_PROJECT_NAME["']?[ \t]*[=:][ \t]*["']?([^\s"';&|]+)`)
	ciVarRef     = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
	// ciRawHostVar finds "NAME_HOST: value" lines when the YAML does not
	// parse.
	ciRawHostVar = regexp.MustCompile(`(?m)^[ \t]*([A-Z][A-Z0-9_]*_HOST)[ \t]*:[ \t]*["']?([^\s"'#]+)`)
	ciProject    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
)

// ciScope is one job's view of the variables: the job's own over the global
// ones.
type ciScope map[string]string

// ciVar is a *_HOST variable in the order the file defines it.
type ciVar struct {
	name  string
	value string
	scope ciScope
}

// ciTemplate is a DOCKER_HOST value with the variables it was found under.
// The scope is the live map of its job, so the value expands with every
// variable of the job, also those defined after it.
type ciTemplate struct {
	value string
	scope ciScope
}

// ciParse is what a walk of the file gathers.
type ciParse struct {
	hostVars  []ciVar
	seenVar   map[string]bool
	templates []ciTemplate
	projects  []string
	notes     []string
	// visits and scanBytes are what is left of ciMaxVisits and
	// ciMaxScanBytes; over is set when one ran out and the walk stopped.
	visits    int
	scanBytes int
	over      bool
}

// visit spends one node of the parse's budget, and says whether the walk may
// go on.
func (p *ciParse) visit() bool {
	if p.visits <= 0 {
		p.over = true
		return false
	}
	p.visits--
	return true
}

// ParseGitLabCI reads a .gitlab-ci.yml for log watch candidates: *_HOST
// variables as hosts (port 2375 unless a DOCKER_HOST line that uses them
// gives another), DOCKER_HOST=tcp://… and DOCKER_HOST: tcp://… in variables
// and scripts as endpoints, with $VAR and ${VAR} expanded from the file's
// variables (top-level and the job's own, plain or {value: …}), and the -p
// or --project-name of docker-compose and docker compose and
// COMPOSE_PROJECT_NAME as compose projects. It does not follow include:.
// A file that is not YAML is searched as text.
func ParseGitLabCI(data []byte) CICandidates {
	if len(data) > CIMaxBytes {
		return CICandidates{Notes: []string{"파일이 256KiB 를 넘어 읽지 않았다."}}
	}
	p := &ciParse{seenVar: map[string]bool{}, visits: ciMaxVisits, scanBytes: ciMaxScanBytes}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		if err != nil {
			p.notes = append(p.notes, "YAML 로 읽지 못해 원문에서만 후보를 찾았다.")
		}
		p.raw(string(data))
	} else {
		p.walk(doc.Content[0])
	}
	return p.candidates()
}

// walk reads the top-level mapping: the global variables and scripts, then
// each job under its own scope.
func (p *ciParse) walk(root *yaml.Node) {
	global := ciScope{}
	p.addVars(global, p.get(root, "variables", 0), 0)
	if p.get(root, "include", 0) != nil {
		p.notes = append(p.notes, "include: 로 불러오는 파일은 따라가지 않았다. 그 파일의 변수·스크립트는 후보에 없다.")
	}
	for _, key := range []string{"default", "before_script", "after_script"} {
		if n := p.get(root, key, 0); n != nil {
			p.scan(n, global, map[*yaml.Node]bool{}, 0)
		}
	}

	for i := 0; i+1 < len(root.Content) && !p.over; i += 2 {
		name, job := root.Content[i].Value, ciDeref(root.Content[i+1])
		if ciKeywords[name] || name == "<<" || job == nil || job.Kind != yaml.MappingNode {
			continue
		}
		scope := ciScope{}
		for k, v := range global {
			scope[k] = v
		}
		p.jobVars(root, job, scope, 0)
		p.scan(job, scope, map[*yaml.Node]bool{}, 0)
	}
}

// jobVars adds a job's variables to its scope, after those of the jobs it
// extends.
func (p *ciParse) jobVars(root, job *yaml.Node, scope ciScope, depth int) {
	if depth > 5 || !p.visit() {
		return
	}
	if ext := p.get(job, "extends", 0); ext != nil {
		var names []string
		switch ext.Kind {
		case yaml.ScalarNode:
			names = []string{ext.Value}
		case yaml.SequenceNode:
			for _, n := range ext.Content {
				names = append(names, n.Value)
			}
		}
		for _, name := range names {
			if parent := p.get(root, name, 0); parent != nil && parent.Kind == yaml.MappingNode {
				p.jobVars(root, parent, scope, depth+1)
			}
		}
	}
	p.addVars(scope, p.get(job, "variables", 0), 0)
}

// addVars reads a variables mapping into a scope, and notes its *_HOST
// variables, DOCKER_HOST and COMPOSE_PROJECT_NAME.
func (p *ciParse) addVars(scope ciScope, vars *yaml.Node, depth int) {
	vars = ciDeref(vars)
	if vars == nil || vars.Kind != yaml.MappingNode || depth > ciMaxDepth || !p.visit() {
		return
	}
	for i := 0; i+1 < len(vars.Content); i += 2 {
		if !p.visit() {
			return
		}
		key, val := vars.Content[i].Value, ciDeref(vars.Content[i+1])
		if key == "<<" {
			p.addMerge(scope, val, depth)
			continue
		}
		value, ok := p.varValue(val)
		if !ok {
			continue
		}
		scope[key] = value
	}
	for i := 0; i+1 < len(vars.Content); i += 2 {
		key := vars.Content[i].Value
		value, ok := scope[key]
		if !ok {
			continue
		}
		switch {
		case key == "DOCKER_HOST":
			if strings.HasPrefix(value, "tcp://") {
				p.templates = append(p.templates, ciTemplate{value: value, scope: scope})
			}
		case key == "COMPOSE_PROJECT_NAME":
			p.projects = append(p.projects, ciExpand(value, scope))
		case strings.HasSuffix(key, "_HOST") && !p.seenVar[key]:
			p.seenVar[key] = true
			p.hostVars = append(p.hostVars, ciVar{name: key, value: value, scope: scope})
		}
	}
}

func (p *ciParse) addMerge(scope ciScope, val *yaml.Node, depth int) {
	if val == nil {
		return
	}
	if val.Kind == yaml.SequenceNode {
		for _, n := range val.Content {
			p.addVars(scope, n, depth+1)
		}
		return
	}
	p.addVars(scope, val, depth+1)
}

// scan reads every string under a node: DOCKER_HOST settings and compose
// project names in scripts. A node seen under the same scope already gave
// what it has, so an alias to it is not read again.
func (p *ciParse) scan(n *yaml.Node, scope ciScope, seen map[*yaml.Node]bool, depth int) {
	n = ciDeref(n)
	if n == nil || depth > ciMaxDepth || seen[n] || !p.visit() {
		return
	}
	seen[n] = true
	switch n.Kind {
	case yaml.ScalarNode:
		if len(n.Value) > p.scanBytes {
			p.over = true
			return
		}
		p.scanBytes -= len(n.Value)
		p.text(n.Value, scope)
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			// Variables are read by addVars; a DOCKER_HOST there is not
			// a script line.
			if n.Content[i].Value == "variables" {
				continue
			}
			p.scan(n.Content[i+1], scope, seen, depth+1)
		}
	case yaml.SequenceNode, yaml.DocumentNode:
		for _, c := range n.Content {
			p.scan(c, scope, seen, depth+1)
		}
	}
}

// text reads one script string.
func (p *ciParse) text(s string, scope ciScope) {
	for _, m := range ciDockerHost.FindAllStringSubmatch(s, -1) {
		p.templates = append(p.templates, ciTemplate{value: m[1], scope: scope})
	}
	for _, m := range ciCompose.FindAllStringSubmatch(s, -1) {
		p.projects = append(p.projects, ciExpand(m[1], scope))
	}
	for _, m := range ciComposeEnv.FindAllStringSubmatch(s, -1) {
		p.projects = append(p.projects, ciExpand(m[1], scope))
	}
}

// raw searches a file that is not YAML as text, with every NAME_HOST line it
// finds as a variable.
func (p *ciParse) raw(data string) {
	scope := ciScope{}
	for _, m := range ciRawHostVar.FindAllStringSubmatch(data, -1) {
		if _, ok := scope[m[1]]; ok {
			continue
		}
		scope[m[1]] = m[2]
		if m[1] != "DOCKER_HOST" {
			p.seenVar[m[1]] = true
			p.hostVars = append(p.hostVars, ciVar{name: m[1], value: m[2], scope: scope})
		}
	}
	p.text(data, scope)
}

// candidates turns what the walk gathered into the answer.
func (p *ciParse) candidates() CICandidates {
	out := CICandidates{Notes: p.notes}
	if p.over {
		out.Notes = append(out.Notes, "별칭·병합 키가 너무 많이 펼쳐져 파일의 앞부분만 읽었다. 후보가 빠졌을 수 있다.")
	}
	seen := map[string]bool{}
	add := func(h CIHost) {
		key := h.Endpoint
		if h.Unresolved != "" {
			key = h.Variable + "\n" + h.Endpoint
		}
		if seen[key] {
			return
		}
		seen[key] = true
		out.Hosts = append(out.Hosts, h)
	}

	// Templates by the *_HOST variable they use.
	byVar := map[string][]ciTemplate{}
	var loose []ciTemplate
	for _, t := range p.templates {
		ref := ""
		for _, m := range ciVarRef.FindAllStringSubmatch(t.value, -1) {
			if name := m[1] + m[2]; strings.HasSuffix(name, "_HOST") {
				ref = name
				break
			}
		}
		if ref == "" {
			loose = append(loose, t)
			continue
		}
		if !p.seenVar[ref] {
			p.seenVar[ref] = true
			p.hostVars = append(p.hostVars, ciVar{name: ref, scope: t.scope})
		}
		byVar[ref] = append(byVar[ref], t)
	}

	for _, v := range p.hostVars {
		display := ciDisplayName(v.name)
		if ts := byVar[v.name]; len(ts) > 0 {
			for _, t := range ts {
				endpoint := ciEndpoint(ciExpand(t.value, t.scope))
				add(CIHost{Variable: v.name, DisplayName: display, Endpoint: endpoint, Unresolved: ciUnresolved(endpoint)})
			}
			continue
		}
		value := ciExpand(v.value, v.scope)
		if !strings.HasPrefix(value, "tcp://") {
			value = "tcp://" + value
		}
		endpoint := ciEndpoint(value)
		add(CIHost{Variable: v.name, DisplayName: display, Endpoint: endpoint, Unresolved: ciUnresolved(endpoint)})
	}

	n := 0
	for _, t := range loose {
		endpoint := ciEndpoint(ciExpand(t.value, t.scope))
		if seen[endpoint] {
			continue
		}
		n++
		display := "docker-host"
		if n > 1 {
			display += "-" + strconv.Itoa(n)
		}
		add(CIHost{Variable: "DOCKER_HOST", DisplayName: display, Endpoint: endpoint, Unresolved: ciUnresolved(endpoint)})
	}

	projectSeen := map[string]bool{}
	for _, name := range p.projects {
		name = strings.Trim(name, `"'`)
		if projectSeen[name] {
			continue
		}
		projectSeen[name] = true
		if !ciProject.MatchString(name) {
			out.Notes = append(out.Notes, "compose 프로젝트 이름을 풀지 못했다: "+CleanKind(name))
			continue
		}
		out.ComposeProjects = append(out.ComposeProjects, name)
	}
	return out
}

// get finds a key in a mapping, also through "<<" merge keys, on the parse's
// visit budget.
func (p *ciParse) get(m *yaml.Node, key string, depth int) *yaml.Node {
	m = ciDeref(m)
	if m == nil || m.Kind != yaml.MappingNode || depth > ciMaxDepth || !p.visit() {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return ciDeref(m.Content[i+1])
		}
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != "<<" {
			continue
		}
		merge := ciDeref(m.Content[i+1])
		if merge == nil {
			continue
		}
		sources := []*yaml.Node{merge}
		if merge.Kind == yaml.SequenceNode {
			sources = merge.Content
		}
		for _, src := range sources {
			if n := p.get(src, key, depth+1); n != nil {
				return n
			}
		}
	}
	return nil
}

// ciDeref follows an alias to the node it names.
func ciDeref(n *yaml.Node) *yaml.Node {
	for i := 0; n != nil && n.Kind == yaml.AliasNode && i < ciMaxDepth; i++ {
		n = n.Alias
	}
	return n
}

// varValue reads a variable's value: a scalar, or {value: …}.
func (p *ciParse) varValue(n *yaml.Node) (string, bool) {
	if n == nil {
		return "", false
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Value, true
	case yaml.MappingNode:
		if v := p.get(n, "value", 0); v != nil && v.Kind == yaml.ScalarNode {
			return v.Value, true
		}
	}
	return "", false
}

// ciExpand replaces $VAR and ${VAR} with the scope's values, again for
// values that name other variables, and leaves unknown references as they
// are. A value whose expansion would grow by more than ciMaxExpandGrowth
// bytes is given back as it was, so its first reference shows as
// unresolved.
func ciExpand(s string, scope ciScope) string {
	limit := len(s) + ciMaxExpandGrowth
	cur := s
	for range ciMaxExpand {
		next, ok := ciExpandOnce(cur, scope, limit)
		if !ok {
			return s
		}
		if next == cur {
			break
		}
		cur = next
	}
	return cur
}

// ciExpandOnce makes one pass of ciExpand. It stops as soon as the result
// would pass limit bytes and then reports false.
func ciExpandOnce(s string, scope ciScope, limit int) (string, bool) {
	var b strings.Builder
	last := 0
	for _, loc := range ciVarRef.FindAllStringIndex(s, -1) {
		m := s[loc[0]:loc[1]]
		v, ok := scope[strings.Trim(m, "${}")]
		if !ok {
			v = m
		}
		if b.Len()+(loc[0]-last)+len(v) > limit {
			return "", false
		}
		b.WriteString(s[last:loc[0]])
		b.WriteString(v)
		last = loc[1]
	}
	if b.Len()+len(s)-last > limit {
		return "", false
	}
	b.WriteString(s[last:])
	return b.String(), true
}

// ciUnresolved is the first variable reference left in a value.
func ciUnresolved(s string) string {
	if m := ciVarRef.FindString(s); m != "" {
		return m
	}
	return ""
}

// ciEndpoint writes tcp://host[:port] with the default port when it has
// none, without a path.
func ciEndpoint(s string) string {
	s = strings.Trim(strings.TrimSpace(s), `"'`)
	rest := strings.TrimPrefix(s, "tcp://")
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	var hasPort bool
	if strings.HasPrefix(rest, "[") {
		end := strings.Index(rest, "]")
		hasPort = end >= 0 && strings.HasPrefix(rest[end+1:], ":")
	} else {
		hasPort = strings.Contains(rest, ":")
	}
	if !hasPort {
		rest += ":" + ciDefaultPort
	}
	return "tcp://" + rest
}

// ciDisplayName is a variable's name without "_HOST", in lower case with
// dashes.
func ciDisplayName(name string) string {
	name = strings.TrimSuffix(name, "_HOST")
	if name == "" {
		return "docker-host"
	}
	return strings.ReplaceAll(strings.ToLower(name), "_", "-")
}
