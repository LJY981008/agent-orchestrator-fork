package ladologwatch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func dockerTestCompose(tag, name, project, service string) dockerTestContainer {
	return dockerTestContainer{
		ID:        dockerTestID(tag),
		Name:      name,
		Image:     "registry.example.test/" + service + ":1.0",
		Labels:    map[string]string{dockerLabelComposeProject: project, dockerLabelComposeService: service},
		StartedAt: "2026-11-10T01:02:03.000000004Z",
	}
}

func TestDiscoverComposeServiceAcrossHosts(t *testing.T) {
	first := newDockerTestEngine(t, dockerTestCompose("a1", "grp-api-1", "grp", "api"))
	second := dockerTestCompose("b1", "grp-api-2", "grp", "api")
	second.Tty = true
	secondEngine := newDockerTestEngine(t, second, dockerTestCompose("b2", "other-api-1", "other", "api"))
	ctx := context.Background()

	var services []string
	for _, engine := range []*dockerTestEngine{first, secondEngine} {
		s := newDockerTestSource(t, engine.endpoint(), DockerTarget{ID: "t1", Kind: dockerTargetComposeProject, Value: "grp"})
		instances, err := s.Discover(ctx)
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if len(instances) != 1 {
			t.Fatalf("instances = %+v, want one", instances)
		}
		inst := instances[0]
		if inst.TargetID != "t1" || inst.Image != "registry.example.test/api:1.0" {
			t.Fatalf("instance = %+v", inst)
		}
		if want := time.Date(2026, 11, 10, 1, 2, 3, 4, time.UTC); !inst.StartedAt.Equal(want) {
			t.Fatalf("StartedAt = %v, want %v", inst.StartedAt, want)
		}
		services = append(services, inst.Service)
	}
	if services[0] != "grp/api" || services[1] != "grp/api" {
		t.Fatalf("services = %v, want grp/api on both hosts", services)
	}
	// The TTY flag comes from inspect.
	s := newDockerTestSource(t, secondEngine.endpoint(), DockerTarget{ID: "t1", Kind: dockerTargetComposeProject, Value: "grp"})
	instances, _ := s.Discover(ctx)
	if len(instances) != 1 || !instances[0].Tty || instances[0].Name != "grp-api-2" {
		t.Fatalf("instances = %+v, want grp-api-2 with a TTY", instances)
	}
}

func TestDiscoverServiceNameOrder(t *testing.T) {
	cases := []struct {
		name   string
		target DockerTarget
		labels map[string]string
		cname  string
		want   string
	}{
		{name: "override beats labels", target: DockerTarget{Service: "payments"}, labels: map[string]string{dockerLabelComposeProject: "grp", dockerLabelComposeService: "api"}, cname: "grp-api-1", want: "payments"},
		{name: "compose labels", labels: map[string]string{dockerLabelComposeProject: "grp", dockerLabelComposeService: "api"}, cname: "x", want: "grp/api"},
		{name: "service label only", labels: map[string]string{dockerLabelComposeService: "api"}, cname: "x", want: "api"},
		{name: "dash replica", cname: "worker-1", want: "worker"},
		{name: "underscore replica", cname: "worker_2", want: "worker"},
		{name: "no replica", cname: "worker", want: "worker"},
		{name: "only digits after dash", cname: "app-v2", want: "app-v2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dockerServiceName(tc.target, tc.labels, tc.cname); got != tc.want {
				t.Fatalf("dockerServiceName = %q, want %q", got, tc.want)
			}
		})
	}

	// The same order through Discover.
	plain := dockerTestContainer{ID: dockerTestID("w1"), Name: "worker-1", Labels: map[string]string{"tier": "jobs"}}
	plain2 := dockerTestContainer{ID: dockerTestID("w2"), Name: "worker_2", Labels: map[string]string{"tier": "jobs"}}
	composed := dockerTestCompose("c1", "grp-api-1", "grp", "api")
	engine := newDockerTestEngine(t, plain, plain2, composed)
	s := newDockerTestSource(t, engine.endpoint(),
		DockerTarget{ID: "jobs", Kind: dockerTargetLabel, Value: "tier=jobs"},
		DockerTarget{ID: "api", Kind: dockerTargetComposeService, Value: "api", ComposeProject: "grp", Service: "front"},
	)
	instances, err := s.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	got := map[string]string{}
	for _, inst := range instances {
		got[inst.Name] = inst.Service
	}
	want := map[string]string{"worker-1": "worker", "worker_2": "worker", "grp-api-1": "front"}
	if len(got) != len(want) {
		t.Fatalf("services = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("services = %v, want %v", got, want)
		}
	}
}

func TestDiscoverNameTargetIsExact(t *testing.T) {
	engine := newDockerTestEngine(t,
		dockerTestContainer{ID: dockerTestID("n1"), Name: "api"},
		dockerTestContainer{ID: dockerTestID("n2"), Name: "api-gateway"},
		dockerTestContainer{ID: dockerTestID("n3"), Name: "payment-api-1"},
	)
	engine.looseNames = true
	s := newDockerTestSource(t, engine.endpoint(), DockerTarget{ID: "t1", Kind: dockerTargetName, Value: "api"})
	instances, err := s.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(instances) != 1 || instances[0].Name != "api" {
		t.Fatalf("instances = %+v, want only api", instances)
	}
	var sent map[string][]string
	for _, r := range engine.recorded() {
		if r.Path == "/containers/json" {
			if err := json.Unmarshal([]byte(r.Query.Get("filters")), &sent); err != nil {
				t.Fatalf("filters %q: %v", r.Query.Get("filters"), err)
			}
			if r.Query.Has("all") {
				t.Fatal("Discover asked for stopped containers too")
			}
		}
		// The loose engine listed all three; only api is inspected.
		if strings.HasSuffix(r.Path, "/json") && r.Path != "/containers/json" && r.Path != "/containers/"+dockerTestID("n1")+"/json" {
			t.Fatalf("Discover inspected %s", r.Path)
		}
	}
	if len(sent["name"]) != 1 || sent["name"][0] != "^/api$" {
		t.Fatalf("name filter = %v, want ^/api$", sent)
	}

	// A value with regular expression characters is quoted.
	filters, err := dockerFilters(DockerTarget{Kind: dockerTargetName, Value: "a.b"})
	if err != nil || filters != `{"name":["^/a\\.b$"]}` {
		t.Fatalf("filters = %s, %v", filters, err)
	}
}

func TestDiscoverOverlappingTargetsMakeOneInstance(t *testing.T) {
	api := dockerTestCompose("o1", "grp-api-1", "grp", "api")
	web := dockerTestCompose("o2", "grp-web-1", "grp", "web")
	engine := newDockerTestEngine(t, api, web)
	s := newDockerTestSource(t, engine.endpoint(),
		DockerTarget{ID: "byName", Kind: dockerTargetName, Value: "grp-api-1", Service: "named"},
		DockerTarget{ID: "byProject", Kind: dockerTargetComposeProject, Value: "grp"},
	)
	instances, err := s.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("instances = %+v, want two", instances)
	}
	if instances[0].ID != api.ID || instances[0].TargetID != "byName" || instances[0].Service != "named" {
		t.Fatalf("first instance = %+v, want grp-api-1 from the first target", instances[0])
	}
	if instances[1].ID != web.ID || instances[1].TargetID != "byProject" || instances[1].Service != "grp/web" {
		t.Fatalf("second instance = %+v", instances[1])
	}
	inspected := 0
	for _, r := range engine.recorded() {
		if r.Path == "/containers/"+api.ID+"/json" {
			inspected++
		}
	}
	if inspected != 1 {
		t.Fatalf("grp-api-1 inspected %d times, want once", inspected)
	}
}

func TestDiscoverChecksFiltersItself(t *testing.T) {
	// A compose_service target narrowed to one project, a label key and a
	// label key=value.
	a := dockerTestCompose("f1", "grp-api-1", "grp", "api")
	b := dockerTestCompose("f2", "other-api-1", "other", "api")
	c := dockerTestContainer{ID: dockerTestID("f3"), Name: "cron", Labels: map[string]string{"tier": "batch"}}
	engine := newDockerTestEngine(t, a, b, c)
	ctx := context.Background()

	s := newDockerTestSource(t, engine.endpoint(), DockerTarget{ID: "t", Kind: dockerTargetComposeService, Value: "api", ComposeProject: "grp"})
	instances, err := s.Discover(ctx)
	if err != nil || len(instances) != 1 || instances[0].ID != a.ID {
		t.Fatalf("compose_service with project = %+v, %v", instances, err)
	}
	s = newDockerTestSource(t, engine.endpoint(), DockerTarget{ID: "t", Kind: dockerTargetComposeService, Value: "api"})
	if instances, _ = s.Discover(ctx); len(instances) != 2 {
		t.Fatalf("compose_service without project = %+v", instances)
	}
	s = newDockerTestSource(t, engine.endpoint(), DockerTarget{ID: "t", Kind: dockerTargetLabel, Value: "tier"})
	if instances, _ = s.Discover(ctx); len(instances) != 1 || instances[0].ID != c.ID {
		t.Fatalf("label key = %+v", instances)
	}
	s = newDockerTestSource(t, engine.endpoint(), DockerTarget{ID: "t", Kind: dockerTargetLabel, Value: "tier=web"})
	if instances, _ = s.Discover(ctx); len(instances) != 0 {
		t.Fatalf("label key=value = %+v", instances)
	}

	// The package's own check refuses what a looser Engine let through.
	listed := dockerListed{ID: a.ID, Names: []string{"/grp-api-1"}, Labels: map[string]string{dockerLabelComposeProject: "grp-x"}}
	if dockerMatches(DockerTarget{Kind: dockerTargetComposeProject, Value: "grp"}, listed) {
		t.Fatal("project grp matched grp-x")
	}
	if dockerMatches(DockerTarget{Kind: "unknown", Value: "grp"}, listed) {
		t.Fatal("an unknown kind matched")
	}
	if _, err := dockerFilters(DockerTarget{Kind: "unknown", Value: "x"}); err == nil {
		t.Fatal("an unknown kind made filters")
	}
}

func TestDiscoverSkipsGoneAndNeedsTargets(t *testing.T) {
	a := dockerTestCompose("g1", "grp-api-1", "grp", "api")
	b := dockerTestCompose("g2", "grp-api-2", "grp", "api")
	engine := newDockerTestEngine(t, a, b)
	engine.gone[a.ID] = true
	s := newDockerTestSource(t, engine.endpoint(), DockerTarget{ID: "t", Kind: dockerTargetComposeProject, Value: "grp"})
	instances, err := s.Discover(context.Background())
	if err != nil || len(instances) != 1 || instances[0].ID != b.ID {
		t.Fatalf("Discover = %+v, %v; want only the container still there", instances, err)
	}

	empty := newDockerTestEngine(t, a)
	s = newDockerTestSource(t, empty.endpoint())
	instances, err = s.Discover(context.Background())
	if err != nil || len(instances) != 0 {
		t.Fatalf("Discover with no targets = %+v, %v", instances, err)
	}
	if n := len(empty.recorded()); n != 0 {
		t.Fatalf("Discover with no targets made %d requests", n)
	}
}
