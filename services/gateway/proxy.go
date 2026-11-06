package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/khayode/email-platform/internal/kit"
)

// Gateway is the single public entry point. It routes browser and API traffic
// to the owning service and strips any identity headers a client might forge.
type Gateway struct {
	cfg      Config
	logger   *slog.Logger
	proxy    *httputil.ReverseProxy
	upstream map[string]*url.URL
	handler  http.Handler
}

// New builds the gateway handler for the given configuration.
func New(cfg Config, logger *slog.Logger) (*Gateway, error) {
	if logger == nil {
		logger = slog.Default()
	}
	upstream := map[string]*url.URL{}
	for name, raw := range map[string]string{
		TargetIdentity:  cfg.IdentityURL,
		TargetWorkspace: cfg.WorkspaceURL,
		TargetLegacy:    cfg.LegacyAPIURL,
	} {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, errInvalidUpstream(name, raw)
		}
		upstream[name] = parsed
	}

	g := &Gateway{cfg: cfg, logger: logger, upstream: upstream}
	g.proxy = &httputil.ReverseProxy{
		Rewrite:      g.rewrite,
		ErrorHandler: g.handleError,
		// The gateway owns CORS (kit.CORS in buildHandler). Upstream services
		// may emit their own Access-Control-* headers; ReverseProxy copies
		// them with Add, which would duplicate Access-Control-Allow-Origin
		// next to the gateway's own value — and browsers reject responses
		// where the header contains more than one value.
		ModifyResponse: stripUpstreamCORSHeaders,
	}
	g.handler = g.buildHandler()
	return g, nil
}

var upstreamCORSHeaders = []string{
	"Access-Control-Allow-Origin",
	"Access-Control-Allow-Credentials",
	"Access-Control-Allow-Headers",
	"Access-Control-Allow-Methods",
	"Access-Control-Expose-Headers",
	"Access-Control-Max-Age",
}

func stripUpstreamCORSHeaders(resp *http.Response) error {
	for _, header := range upstreamCORSHeaders {
		resp.Header.Del(header)
	}
	return nil
}

func errInvalidUpstream(name, raw string) error {
	return errors.New("invalid upstream url for " + name + ": " + raw)
}

// rewrite is the single choke point where paths, hosts, and headers are set
// for every proxied request.
func (g *Gateway) rewrite(pr *httputil.ProxyRequest) {
	target, upstreamPath := route(pr.In.URL.Path)
	parsed := g.upstream[target]

	pr.Out.URL.Scheme = parsed.Scheme
	pr.Out.URL.Host = parsed.Host
	pr.Out.URL.Path = upstreamPath
	pr.Out.URL.RawPath = ""
	pr.Out.Host = parsed.Host

	// Never trust client-supplied identity or service headers. The downstream
	// services authenticate with JWTs and service tokens themselves.
	for _, header := range []string{
		"X-User-ID", "X-User-Email", "X-Org-ID", "X-Organization-ID",
		"X-Role", "X-Service-Token", "X-Forwarded-For", "X-Forwarded-Host",
		"X-Forwarded-Proto", "X-Real-IP",
	} {
		pr.Out.Header.Del(header)
	}
	// Re-derive forwarding headers from the actual client connection.
	pr.SetXForwarded()
}

func (g *Gateway) handleError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(r.Context().Err(), context.Canceled) {
		return
	}
	g.logger.Warn("upstream request failed",
		"error", err,
		"path", r.URL.Path,
		"request_id", r.Header.Get("X-Request-ID"),
	)
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timeout") {
		kit.Error(w, http.StatusGatewayTimeout, "upstream_timeout", "The service took too long to respond.")
		return
	}
	kit.Error(w, http.StatusBadGateway, "upstream_unavailable", "The service is temporarily unavailable.")
}

func (g *Gateway) buildHandler() http.Handler {
	mux := http.NewServeMux()

	authRate := kit.RateLimit(kit.NewRateLimiter(g.cfg.AuthRatePerMinute, g.cfg.AuthBurst))

	mux.Handle("/healthz", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		kit.JSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "gateway"})
	}))
	mux.Handle("/readyz", http.HandlerFunc(g.ready))
	mux.Handle("/metrics", kit.MetricsHandler())

	// Auth endpoints get an extra abuse-protection layer on top of Identity's.
	mux.Handle("/api/v1/auth/", authRate(g.proxy))
	mux.Handle("/api/", g.proxy)
	mux.Handle("/", g.proxy)

	return kit.Chain(
		mux,
		kit.CORS(g.cfg.CORSOrigins),
		kit.Metrics("gateway"),
		kit.Recover(g.logger),
		kit.AccessLog(g.logger),
		kit.RequestID,
	)
}

func (g *Gateway) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	// Every upstream in the canonical stack is required: identity and
	// workspace for auth/tenancy, legacy for campaigns/contacts/analytics.
	// Report each upstream's reachability so operators can see exactly what
	// is down (including legacy).
	upstreams := map[string]string{}
	down := []string{}
	// Probe in a stable order for readable logs.
	for _, name := range []string{TargetIdentity, TargetWorkspace, TargetLegacy} {
		target, ok := g.upstream[name]
		if !ok {
			upstreams[name] = "unconfigured"
			down = append(down, name)
			continue
		}
		probeURL := target.Scheme + "://" + target.Host + "/healthz"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
		if err != nil {
			upstreams[name] = "error"
			down = append(down, name)
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil {
				resp.Body.Close()
			}
			upstreams[name] = "down"
			down = append(down, name)
			continue
		}
		resp.Body.Close()
		upstreams[name] = "up"
	}
	if len(down) > 0 {
		kit.Error(w, http.StatusServiceUnavailable, "not_ready", "Upstream(s) not ready: "+strings.Join(down, ", "))
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{"status": "ready", "upstreams": upstreams})
}

// ServeHTTP implements http.Handler.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.handler.ServeHTTP(w, r)
}
