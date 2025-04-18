package kubernetes

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"text/template"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zalando/skipper/eskip"
	"github.com/zalando/skipper/filters"
	"github.com/zalando/skipper/secrets/certregistry"
)

const (
	defaultIngressClass          = "skipper"
	defaultRouteGroupClass       = "skipper"
	serviceHostEnvVar            = "KUBERNETES_SERVICE_HOST"
	servicePortEnvVar            = "KUBERNETES_SERVICE_PORT"
	httpRedirectRouteID          = "kube__redirect"
	defaultLoadBalancerAlgorithm = "roundRobin"
	defaultEastWestDomain        = "skipper.cluster.local"
)

// PathMode values are used to control the ingress path interpretation. The path mode can
// be set globally for all ingress paths, and it can be overruled by the individual ingress
// rules using the zalando.org/skipper-ingress-path-mode annotation. When path mode is not
// set, the Kubernetes ingress specification is used, accepting regular expressions with a
// mandatory leading "/", automatically prepended by the "^" control character.
//
// When PathPrefix is used, the path matching becomes deterministic when
// a request could match more than one ingress routes otherwise.
type PathMode int

const (
	// KubernetesIngressMode is the default path mode. Expects regular expressions
	// with a mandatory leading "/". The expressions are automatically prepended by
	// the "^" control character.
	KubernetesIngressMode PathMode = iota

	// PathRegexp is like KubernetesIngressMode but is not prepended by the "^"
	// control character.
	PathRegexp

	// PathPrefix is like the PathSubtree predicate. E.g. "/foo/bar" will match
	// "/foo/bar" or "/foo/bar/baz", but won't match "/foo/barooz".
	//
	// In this mode, when a Path or a PathSubtree predicate is set in an annotation,
	// the value from the annotation has precedence over the standard ingress path.
	PathPrefix
)

const (
	kubernetesIngressModeString = "kubernetes-ingress"
	pathRegexpString            = "path-regexp"
	pathPrefixString            = "path-prefix"
)

const maxFileSize = 1024 * 1024 // 1MB

var internalIPs = []interface{}{
	"10.0.0.0/8",
	"192.168.0.0/16",
	"172.16.0.0/12",
	"127.0.0.1/8",
	"fd00::/8",
	"::1/128",
}

// Options is used to initialize the Kubernetes DataClient.
type Options struct {
	// KubernetesInCluster defines if skipper is deployed and running in the kubernetes cluster
	// this would make authentication with API server happen through the service account, rather than
	// running along side kubectl proxy
	KubernetesInCluster bool

	// KubernetesURL is used as the base URL for Kubernetes API requests. Defaults to http://localhost:8001.
	// (TBD: support in-cluster operation by taking the address and certificate from the standard Kubernetes
	// environment variables.)
	KubernetesURL string

	// KubernetesNamespace is used to switch between finding ingresses in the cluster-scope or limit
	// the ingresses to only those in the specified namespace. Defaults to "" which means monitor ingresses
	// in the cluster-scope.
	KubernetesNamespace string

	// *DEPRECATED* KubernetesIngressV1 is used to switch between v1beta1 and v1. Kubernetes version 1.22 stopped support
	// for v1beta1, so we have to provide a migration path and this will someday become the default.
	KubernetesIngressV1 bool

	// *DEPRECATED* KubernetesEnableEastWest if set adds automatically routes
	// with "%s.%s.skipper.cluster.local" domain pattern
	KubernetesEnableEastWest bool

	// ProvideHealthcheck, when set, tells the data client to append a healthcheck route to the ingress
	// routes in case of successfully receiving the ingress items from the API (even if individual ingress
	// items may be invalid), or a failing healthcheck route when the API communication fails. The
	// healthcheck endpoint can be accessed from internal IPs on any hostname, with the path
	// /kube-system/healthz.
	//
	// When used in a custom configuration, the current filter registry needs to include the status()
	// filter, and the available predicates need to include the Source() predicate.
	ProvideHealthcheck bool

	// ProvideHTTPSRedirect, when set, tells the data client to append an HTTPS redirect route to the
	// ingress routes. This route will detect the X-Forwarded-Proto=http and respond with a 301 message
	// to the HTTPS equivalent of the same request (using the redirectTo(301, "https:") filter). The
	// X-Forwarded-Proto and X-Forwarded-Port is expected to be set by the load balancer.
	//
	// (See also https://github.com/zalando-incubator/kube-ingress-aws-controller as part of the
	// https://github.com/zalando-incubator/kubernetes-on-aws project.)
	ProvideHTTPSRedirect bool

	// HTTPSRedirectCode, when set defines which redirect code to use for redirecting from HTTP to HTTPS.
	// By default, 308 StatusPermanentRedirect is used.
	HTTPSRedirectCode int

	// IngressClass is a regular expression to filter only those ingresses that match. If an ingress does
	// not have a class annotation or the annotation is an empty string, skipper will load it. The default
	// value for the ingress class is 'skipper'.
	//
	// For further information see:
	//		https://github.com/nginxinc/kubernetes-ingress/tree/master/examples/multiple-ingress-controllers
	IngressClass string

	// RouteGroupClass is a regular expression to filter only those RouteGroups that match. If a RouteGroup
	// does not have the required annotation (zalando.org/routegroup.class) or the annotation is an empty string,
	// skipper will load it. The default value for the RouteGroup class is 'skipper'.
	RouteGroupClass string

	// ReverseSourcePredicate set to true will do the Source IP
	// whitelisting for the heartbeat endpoint correctly in AWS.
	// Amazon's ALB writes the client IP to the last item of the
	// string list of the X-Forwarded-For header, in this case you
	// want to set this to true.
	ReverseSourcePredicate bool

	// Noop, WIP.
	ForceFullUpdatePeriod time.Duration

	// WhitelistedHealthcheckCIDR to be appended to the default iprange
	WhitelistedHealthCheckCIDR []string

	// PathMode controls the default interpretation of ingress paths in cases when the ingress doesn't
	// specify it with an annotation.
	PathMode PathMode

	// *DEPRECATED *KubernetesEastWestDomain sets the DNS domain to be
	// used for east west traffic, defaults to "skipper.cluster.local"
	KubernetesEastWestDomain string

	// KubernetesEastWestRangeDomains set the the cluster internal domains for
	// east west traffic. Identified routes to such domains will include
	// the KubernetesEastWestRangePredicates.
	KubernetesEastWestRangeDomains []string

	// KubernetesEastWestRangePredicates set the Predicates that will be
	// appended to routes identified as to KubernetesEastWestRangeDomains.
	KubernetesEastWestRangePredicates []*eskip.Predicate

	// DefaultFiltersDir enables default filters mechanism and sets the location of the default filters.
	// The provided filters are then applied to all routes.
	DefaultFiltersDir string

	// OriginMarker is *deprecated* and not used anymore. It will be deleted in v1.
	OriginMarker bool

	// If the OpenTracing tag containing RouteGroup backend name
	// (using tracingTag filter) should be added to all routes
	BackendNameTracingTag bool

	// OnlyAllowedExternalNames will enable validation of ingress external names and route groups network
	// backend addresses, explicit LB endpoints validation agains the list of patterns in
	// AllowedExternalNames.
	OnlyAllowedExternalNames bool

	// AllowedExternalNames contains regexp patterns of those domain names that are allowed to be
	// used with external name services (type=ExternalName).
	AllowedExternalNames []*regexp.Regexp

	CertificateRegistry *certregistry.CertRegistry
}

// Client is a Skipper DataClient implementation used to create routes based on Kubernetes Ingress settings.
type Client struct {
	ClusterClient          *clusterClient
	ingress                *ingress
	routeGroups            *routeGroups
	provideHealthcheck     bool
	provideHTTPSRedirect   bool
	reverseSourcePredicate bool
	httpsRedirectCode      int
	current                map[string]*eskip.Route
	quit                   chan struct{}
	defaultFiltersDir      string
}

// New creates and initializes a Kubernetes DataClient.
func New(o Options) (*Client, error) {
	if !o.KubernetesIngressV1 {
		log.Warning("You are using a deprecated version of ingress. Skipper will remove support in the next couple of weeks. Kubernetes version 1.18 deprecated v1beta1 and 1.22 stopped support v1beta1. We will increase skipper's minor version to highlight the deletion. For more details please check https://opensource.zalando.com/skipper/kubernetes/ingress-controller/#upgrades")
	}
	if o.OriginMarker {
		log.Warning("OriginMarker is deprecated")
	}
	quit := make(chan struct{})

	apiURL, err := buildAPIURL(o)
	if err != nil {
		return nil, err
	}

	ingCls := defaultIngressClass
	if o.IngressClass != "" {
		ingCls = o.IngressClass
	}

	rgCls := defaultRouteGroupClass
	if o.RouteGroupClass != "" {
		rgCls = o.RouteGroupClass
	}

	log.Debugf(
		"running in-cluster: %t. api server url: %s. provide health check: %t. ingress.class filter: %s. routegroup.class filter: %s. namespace: %s",
		o.KubernetesInCluster, apiURL, o.ProvideHealthcheck, ingCls, rgCls, o.KubernetesNamespace,
	)

	if len(o.WhitelistedHealthCheckCIDR) > 0 {
		whitelistCIDRS := make([]interface{}, len(o.WhitelistedHealthCheckCIDR))
		for i, v := range o.WhitelistedHealthCheckCIDR {
			whitelistCIDRS[i] = v
		}
		internalIPs = append(internalIPs, whitelistCIDRS...)
		log.Debugf("new internal ips are: %s", internalIPs)
	}

	if o.HTTPSRedirectCode <= 0 {
		o.HTTPSRedirectCode = http.StatusPermanentRedirect
	}

	if o.KubernetesEnableEastWest {
		if o.KubernetesEastWestDomain == "" {
			o.KubernetesEastWestDomain = defaultEastWestDomain
		} else {
			o.KubernetesEastWestDomain = strings.Trim(o.KubernetesEastWestDomain, ".")
		}
	}

	clusterClient, err := newClusterClient(o, apiURL, ingCls, rgCls, quit)
	if err != nil {
		return nil, err
	}

	if !o.OnlyAllowedExternalNames {
		o.AllowedExternalNames = []*regexp.Regexp{regexp.MustCompile(".*")}
	}

	ing := newIngress(o)
	rg := newRouteGroups(o)

	return &Client{
		ClusterClient:          clusterClient,
		ingress:                ing,
		routeGroups:            rg,
		provideHealthcheck:     o.ProvideHealthcheck,
		provideHTTPSRedirect:   o.ProvideHTTPSRedirect,
		httpsRedirectCode:      o.HTTPSRedirectCode,
		current:                make(map[string]*eskip.Route),
		reverseSourcePredicate: o.ReverseSourcePredicate,
		quit:                   quit,
		defaultFiltersDir:      o.DefaultFiltersDir,
	}, nil
}

func buildAPIURL(o Options) (string, error) {
	if !o.KubernetesInCluster {
		if o.KubernetesURL == "" {
			return defaultKubernetesURL, nil
		}
		return o.KubernetesURL, nil
	}

	host, port := os.Getenv(serviceHostEnvVar), os.Getenv(servicePortEnvVar)
	if host == "" || port == "" {
		return "", errAPIServerURLNotFound
	}

	return "https://" + net.JoinHostPort(host, port), nil
}

// String returns the string representation of the path mode, the same
// values that are used in the path mode annotation.
func (m PathMode) String() string {
	switch m {
	case PathRegexp:
		return pathRegexpString
	case PathPrefix:
		return pathPrefixString
	default:
		return kubernetesIngressModeString
	}
}

// ParsePathMode parses the string representations of the different
// path modes.
func ParsePathMode(s string) (PathMode, error) {
	switch s {
	case kubernetesIngressModeString:
		return KubernetesIngressMode, nil
	case pathRegexpString:
		return PathRegexp, nil
	case pathPrefixString:
		return PathPrefix, nil
	default:
		return 0, fmt.Errorf("invalid path mode string: %s", s)
	}
}

func mapRoutes(r []*eskip.Route) map[string]*eskip.Route {
	m := make(map[string]*eskip.Route)
	for _, ri := range r {
		m[ri.Id] = ri
	}

	return m
}

func (c *Client) loadAndConvert() ([]*eskip.Route, error) {
	state, err := c.ClusterClient.fetchClusterState()
	if err != nil {
		return nil, err
	}

	defaultFilters := c.fetchDefaultFilterConfigs()

	ri, err := c.ingress.convert(state, defaultFilters, c.ClusterClient.certificateRegistry)
	if err != nil {
		return nil, err
	}

	rg, err := c.routeGroups.convert(state, defaultFilters)
	if err != nil {
		return nil, err
	}

	r := append(ri, rg...)

	if c.provideHealthcheck {
		r = append(r, healthcheckRoutes(c.reverseSourcePredicate)...)
	}

	if c.provideHTTPSRedirect {
		r = append(r, globalRedirectRoute(c.httpsRedirectCode))
	}

	return r, nil
}

func shuntRoute(r *eskip.Route) {
	r.Filters = []*eskip.Filter{
		{
			Name: filters.StatusName,
			Args: []interface{}{502.0},
		},
		{
			Name: filters.InlineContentName,
			Args: []interface{}{"no endpoints"},
		},
	}
	r.BackendType = eskip.ShuntBackend
	r.Backend = ""
}

func healthcheckRoutes(reverseSourcePredicate bool) []*eskip.Route {
	template := template.Must(template.New("healthcheck").Parse(`
		kube__healthz_up:   Path("/kube-system/healthz") && {{.Source}}({{.SourceCIDRs}}) -> {{.DisableAccessLog}} status(200) -> <shunt>;
		kube__healthz_down: Path("/kube-system/healthz") && {{.Source}}({{.SourceCIDRs}}) && Shutdown() -> status(503) -> <shunt>;
	`))

	params := struct {
		Source           string
		SourceCIDRs      string
		DisableAccessLog string
	}{}

	if reverseSourcePredicate {
		params.Source = "SourceFromLast"
	} else {
		params.Source = "Source"
	}

	if !log.IsLevelEnabled(log.DebugLevel) {
		params.DisableAccessLog = "disableAccessLog(200) ->"
	}

	cidrs := new(strings.Builder)
	for i, ip := range internalIPs {
		if i > 0 {
			cidrs.WriteString(", ")
		}
		cidrs.WriteString(fmt.Sprintf("%q", ip))
	}
	params.SourceCIDRs = cidrs.String()

	out := new(strings.Builder)
	_ = template.Execute(out, params)
	routes, _ := eskip.Parse(out.String())

	return routes
}

func (c *Client) LoadAll() ([]*eskip.Route, error) {
	log.Debug("loading all")
	r, err := c.loadAndConvert()
	if err != nil {
		return nil, fmt.Errorf("failed to load cluster state: %w", err)
	}

	c.current = mapRoutes(r)
	log.Debugf("all routes loaded and mapped")

	return r, nil
}

// LoadUpdate returns all known eskip.Route, a list of route IDs
// scheduled for delete and an error.
//
// TODO: implement a force reset after some time.
func (c *Client) LoadUpdate() ([]*eskip.Route, []string, error) {
	log.Debugf("polling for updates")
	r, err := c.loadAndConvert()
	if err != nil {
		log.Errorf("polling for updates failed: %v", err)
		return nil, nil, err
	}

	next := mapRoutes(r)
	log.Debugf("next version of routes loaded and mapped")

	var (
		updatedRoutes []*eskip.Route
		deletedIDs    []string
	)

	for id := range c.current {
		// TODO: use eskip.Eq()
		if r, ok := next[id]; ok && r.String() != c.current[id].String() {
			updatedRoutes = append(updatedRoutes, r)
		} else if !ok {
			deletedIDs = append(deletedIDs, id)
		}
	}

	for id, r := range next {
		if _, ok := c.current[id]; !ok {
			updatedRoutes = append(updatedRoutes, r)
		}
	}

	if len(updatedRoutes) > 0 || len(deletedIDs) > 0 {
		log.Infof("diff taken, inserts/updates: %d, deletes: %d", len(updatedRoutes), len(deletedIDs))
	}

	c.current = next
	return updatedRoutes, deletedIDs, nil
}

func (c *Client) Close() {
	if c != nil && c.quit != nil {
		close(c.quit)
	}
}

func (c *Client) fetchDefaultFilterConfigs() defaultFilters {
	if c.defaultFiltersDir == "" {
		log.Debug("default filters are disabled")
		return nil
	}

	filters, err := readDefaultFilters(c.defaultFiltersDir)
	if err != nil {
		log.WithError(err).Error("could not fetch default filter configurations")
		return nil
	}

	log.WithField("#configs", len(filters)).Debug("default filter configurations loaded")
	return filters
}

func compareStringList(a, b []string) []string {
	c := make([]string, 0)
	for i := len(a) - 1; i >= 0; i-- {
		for _, vD := range b {
			if a[i] == vD {
				c = append(c, vD)
				break
			}
		}
	}
	return c
}
