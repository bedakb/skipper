package kubernetestest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"

	yaml2 "github.com/ghodss/yaml"
	"gopkg.in/yaml.v2"

	"github.com/zalando/skipper/dataclients/kubernetes"
)

var errInvalidFixture = errors.New("invalid fixture")

type TestAPIOptions struct {
	FailOn             []string `yaml:"failOn"`
	FindNot            []string `yaml:"findNot"`
	DisableRouteGroups bool     `yaml:"disableRouteGroups"`
}

type namespace struct {
	services    []byte
	ingresses   []byte
	routeGroups []byte
	endpoints   []byte
	secrets     []byte
}

type api struct {
	failOn       map[string]bool
	findNot      map[string]bool
	namespaces   map[string]namespace
	all          namespace
	pathRx       *regexp.Regexp
	resourceList []byte
}

func NewAPI(o TestAPIOptions, specs ...io.Reader) (*api, error) {
	a := &api{
		namespaces: make(map[string]namespace),
		pathRx: regexp.MustCompile(
			"(/namespaces/([^/]+))?/(services|ingresses|routegroups|endpoints|secrets)",
		),
	}

	var clr kubernetes.ClusterResourceList
	if !o.DisableRouteGroups {
		clr.Items = append(clr.Items, &kubernetes.ClusterResource{Name: kubernetes.RouteGroupsName})
	}

	a.failOn = mapStrings(o.FailOn)
	a.findNot = mapStrings(o.FindNot)

	clrb, err := json.Marshal(clr)
	if err != nil {
		return nil, err
	}

	a.resourceList = clrb

	namespaces := make(map[string]map[string][]interface{})
	all := make(map[string][]interface{})

	for _, spec := range specs {
		d := yaml.NewDecoder(spec)
		for {
			var o map[string]interface{}
			if err := d.Decode(&o); err == io.EOF || err == nil && len(o) == 0 {
				break
			} else if err != nil {
				return nil, err
			}

			kind, ok := o["kind"].(string)
			if !ok {
				return nil, errInvalidFixture
			}

			meta, ok := o["metadata"].(map[interface{}]interface{})
			if !ok {
				return nil, errInvalidFixture
			}

			namespace, ok := meta["namespace"]
			if !ok || namespace == "" {
				namespace = "default"
			} else {
				if _, ok := namespace.(string); !ok {
					return nil, errInvalidFixture
				}
			}

			ns := namespace.(string)
			if _, ok := namespaces[ns]; !ok {
				namespaces[ns] = make(map[string][]interface{})
			}

			namespaces[ns][kind] = append(namespaces[ns][kind], o)
			all[kind] = append(all[kind], o)
		}
	}

	for ns, kinds := range namespaces {
		var err error
		a.namespaces[ns], err = initNamespace(kinds)
		if err != nil {
			return nil, err
		}
	}

	a.all, err = initNamespace(all)
	if err != nil {
		return nil, err
	}

	return a, nil
}

func (a *api) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if a.failOn[r.URL.Path] {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if a.findNot[r.URL.Path] {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if r.URL.Path == kubernetes.ZalandoResourcesClusterURI {
		w.Write(a.resourceList)
		return
	}

	parts := a.pathRx.FindStringSubmatch(r.URL.Path)
	if len(parts) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	ns := a.all
	if parts[2] != "" {
		ns = a.namespaces[parts[2]]
	}

	var b []byte
	switch parts[3] {
	case "services":
		b = ns.services
	case "ingresses":
		b = ns.ingresses
	case "routegroups":
		b = ns.routeGroups
	case "endpoints":
		b = ns.endpoints
	case "secrets":
		b = ns.secrets
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.Write(b)
}

func initNamespace(kinds map[string][]interface{}) (ns namespace, err error) {
	if err = itemsJSON(&ns.services, kinds["Service"]); err != nil {
		return
	}

	if err = itemsJSON(&ns.ingresses, kinds["Ingress"]); err != nil {
		return
	}

	if err = itemsJSON(&ns.routeGroups, kinds["RouteGroup"]); err != nil {
		return
	}

	if err = itemsJSON(&ns.endpoints, kinds["Endpoints"]); err != nil {
		return
	}

	if err = itemsJSON(&ns.secrets, kinds["Secret"]); err != nil {
		return
	}

	return
}

func itemsJSON(b *[]byte, o []interface{}) error {
	items := map[string]interface{}{"items": o}

	// converting back to YAML, because we have YAMLToJSON() for bytes, and
	// the data in `o` contains YAML parser style keys of type interface{}
	y, err := yaml.Marshal(items)
	if err != nil {
		return err
	}

	*b, err = yaml2.YAMLToJSON(y)
	return err
}

func readAPIOptions(r io.Reader) (o TestAPIOptions, err error) {
	var b []byte
	b, err = io.ReadAll(r)
	if err != nil {
		return
	}

	err = yaml.Unmarshal(b, &o)
	return
}

func mapStrings(s []string) map[string]bool {
	m := make(map[string]bool)
	for _, si := range s {
		m[si] = true
	}

	return m
}
