package webgrab

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func htmlResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func TestGrabReaderAndGrabDocumentMatchGrab(t *testing.T) {
	type page struct {
		Title string   `grab:".title||title"`
		Links []string `grab:"a[href]" attr:"href" resolve:"url"`
	}

	html := `<html><body><h1 class="title">Example</h1><a href="/one">One</a><a href="/two">Two</a></body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(html))
	}))
	defer server.Close()

	grabber := New()

	var fromURL page
	if err := grabber.Grab(server.URL, &fromURL); err != nil {
		t.Fatalf("grab URL: %v", err)
	}

	var fromReader page
	if err := grabber.GrabReader(server.URL, strings.NewReader(html), &fromReader); err != nil {
		t.Fatalf("grab reader: %v", err)
	}

	var fromDoc page
	if err := grabber.GrabDocument(server.URL, docFromHTML(t, html), &fromDoc); err != nil {
		t.Fatalf("grab document: %v", err)
	}

	if !reflect.DeepEqual(fromURL, fromReader) {
		t.Fatalf("URL and reader results differ: %#v vs %#v", fromURL, fromReader)
	}
	if !reflect.DeepEqual(fromURL, fromDoc) {
		t.Fatalf("URL and document results differ: %#v vs %#v", fromURL, fromDoc)
	}
}

func TestGrabUsesCustomHTTPClientWhenProvided(t *testing.T) {
	type page struct {
		Title string `grab:"title"`
	}

	grabber := New()
	grabber.Timeout = 1
	grabber.MaxRedirects = 1
	grabber.HTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return htmlResponse(req, http.StatusOK, `<html><head><title>Intercepted</title></head></html>`), nil
		}),
	}

	var data page
	if err := grabber.Grab("http://ignored.test", &data); err != nil {
		t.Fatalf("grab with custom client: %v", err)
	}
	if data.Title != "Intercepted" {
		t.Fatalf("expected title %q, got %q", "Intercepted", data.Title)
	}
}

func TestGrabRunsRequestAndResponseHooks(t *testing.T) {
	type page struct {
		Title string `grab:"title"`
	}

	var beforeCalled, afterCalled bool

	grabber := New()
	grabber.AllowedStatusCodes = []int{http.StatusCreated}
	grabber.BeforeRequest = func(req *http.Request) error {
		beforeCalled = true
		req.Header.Set("X-Test", "hooked")
		return nil
	}
	grabber.AfterResponse = func(resp *http.Response) error {
		afterCalled = true
		return nil
	}
	grabber.HTTPClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("X-Test"); got != "hooked" {
				t.Fatalf("expected header %q, got %q", "hooked", got)
			}
			return htmlResponse(req, http.StatusCreated, `<html><head><title>Hooks</title></head></html>`), nil
		}),
	}

	var data page
	if err := grabber.Grab("http://hooks.test", &data); err != nil {
		t.Fatalf("grab with hooks: %v", err)
	}
	if !beforeCalled || !afterCalled {
		t.Fatalf("expected both hooks to run, before=%v after=%v", beforeCalled, afterCalled)
	}
	if data.Title != "Hooks" {
		t.Fatalf("expected title %q, got %q", "Hooks", data.Title)
	}
}

func TestGrabHookErrorsReturnRequestError(t *testing.T) {
	t.Run("before request", func(t *testing.T) {
		sentinel := errors.New("before failed")
		grabber := New()
		grabber.BeforeRequest = func(req *http.Request) error {
			return sentinel
		}

		var data struct {
			Title string `grab:"title"`
		}
		err := grabber.Grab("http://before.test", &data)
		var requestErr *RequestError
		if !errors.As(err, &requestErr) {
			t.Fatalf("expected RequestError, got %v", err)
		}
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected wrapped sentinel error, got %v", err)
		}
	})

	t.Run("after response", func(t *testing.T) {
		sentinel := errors.New("after failed")
		grabber := New()
		grabber.AfterResponse = func(resp *http.Response) error {
			return sentinel
		}
		grabber.HTTPClient = &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return htmlResponse(req, http.StatusOK, `<html><head><title>Ignored</title></head></html>`), nil
			}),
		}

		var data struct {
			Title string `grab:"title"`
		}
		err := grabber.Grab("http://after.test", &data)
		var requestErr *RequestError
		if !errors.As(err, &requestErr) {
			t.Fatalf("expected RequestError, got %v", err)
		}
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected wrapped sentinel error, got %v", err)
		}
	})
}

func TestGrabAllowedStatusCodesAndStatusErrors(t *testing.T) {
	t.Run("allowed non-200", func(t *testing.T) {
		grabber := New()
		grabber.AllowedStatusCodes = []int{http.StatusAccepted}
		grabber.HTTPClient = &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return htmlResponse(req, http.StatusAccepted, `<html><head><title>Accepted</title></head></html>`), nil
			}),
		}

		var data struct {
			Title string `grab:"title"`
		}
		if err := grabber.Grab("http://accepted.test", &data); err != nil {
			t.Fatalf("expected allowed status to parse, got %v", err)
		}
		if data.Title != "Accepted" {
			t.Fatalf("expected title %q, got %q", "Accepted", data.Title)
		}
	})

	t.Run("disallowed status", func(t *testing.T) {
		grabber := New()
		grabber.HTTPClient = &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return htmlResponse(req, http.StatusTeapot, `<html><body>teapot</body></html>`), nil
			}),
		}

		var data struct {
			Title string `grab:"title"`
		}
		err := grabber.Grab("http://teapot.test", &data)
		var statusErr *StatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("expected StatusError, got %v", err)
		}
		if statusErr.StatusCode != http.StatusTeapot {
			t.Fatalf("expected status %d, got %d", http.StatusTeapot, statusErr.StatusCode)
		}
	})
}

func TestGrabRetriesTransientFailures(t *testing.T) {
	t.Run("status code retry", func(t *testing.T) {
		var attempts int32
		grabber := New()
		grabber.RetryCount = 1
		grabber.HTTPClient = &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if atomic.AddInt32(&attempts, 1) == 1 {
					return htmlResponse(req, http.StatusServiceUnavailable, `<html><body>busy</body></html>`), nil
				}
				return htmlResponse(req, http.StatusOK, `<html><head><title>Recovered</title></head></html>`), nil
			}),
		}

		var data struct {
			Title string `grab:"title"`
		}
		if err := grabber.Grab("http://retry-status.test", &data); err != nil {
			t.Fatalf("expected retry to recover, got %v", err)
		}
		if attempts != 2 {
			t.Fatalf("expected 2 attempts, got %d", attempts)
		}
		if data.Title != "Recovered" {
			t.Fatalf("expected title %q, got %q", "Recovered", data.Title)
		}
	})

	t.Run("network retry", func(t *testing.T) {
		var attempts int32
		grabber := New()
		grabber.RetryCount = 1
		grabber.HTTPClient = &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if atomic.AddInt32(&attempts, 1) == 1 {
					return nil, errors.New("temporary network issue")
				}
				return htmlResponse(req, http.StatusOK, `<html><head><title>Recovered</title></head></html>`), nil
			}),
		}

		var data struct {
			Title string `grab:"title"`
		}
		if err := grabber.Grab("http://retry-network.test", &data); err != nil {
			t.Fatalf("expected retry to recover, got %v", err)
		}
		if attempts != 2 {
			t.Fatalf("expected 2 attempts, got %d", attempts)
		}
	})
}

func TestGrabDoesNotRetryScrapeErrors(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		_, _ = w.Write([]byte(`<html><body><div class="body">missing title</div></body></html>`))
	}))
	defer server.Close()

	grabber := New()
	grabber.RetryCount = 3

	var data struct {
		Title string `grab:"title"`
	}
	err := grabber.Grab(server.URL, &data)
	if err == nil {
		t.Fatal("expected scrape error")
	}
	if attempts != 1 {
		t.Fatalf("expected one request attempt, got %d", attempts)
	}
}

func TestGrabBuiltInTypeConversion(t *testing.T) {
	type page struct {
		Enabled  bool        `grab:".enabled"`
		Count    int         `grab:".count"`
		Total    uint        `grab:".total"`
		Score    float64     `grab:".score"`
		Created  time.Time   `grab:".created" layout:"2006/01/02"`
		Flags    []bool      `grab:".flags li"`
		Numbers  []int       `grab:".numbers li"`
		Decimals []float64   `grab:".decimals li"`
		Dates    []time.Time `grab:".dates li" layout:"2006-01-02"`
	}

	html := `
		<div class="enabled">true</div>
		<div class="count">42</div>
		<div class="total">12</div>
		<div class="score">98.5</div>
		<div class="created">2026/04/01</div>
		<ul class="flags"><li>true</li><li>false</li></ul>
		<ul class="numbers"><li>1</li><li>2</li><li>3</li></ul>
		<ul class="decimals"><li>1.5</li><li>2.5</li></ul>
		<ul class="dates"><li>2026-04-01</li><li>2026-04-02</li></ul>
	`

	var data page
	if err := New().GrabReader("https://example.com", strings.NewReader(html), &data); err != nil {
		t.Fatalf("grab typed fields: %v", err)
	}

	if !data.Enabled || data.Count != 42 || data.Total != 12 || data.Score != 98.5 {
		t.Fatalf("unexpected scalar conversion result: %#v", data)
	}
	if data.Created.Format("2006-01-02") != "2026-04-01" {
		t.Fatalf("unexpected time conversion: %v", data.Created)
	}
	if !reflect.DeepEqual(data.Flags, []bool{true, false}) {
		t.Fatalf("unexpected bool slice: %#v", data.Flags)
	}
	if !reflect.DeepEqual(data.Numbers, []int{1, 2, 3}) {
		t.Fatalf("unexpected int slice: %#v", data.Numbers)
	}
	if !reflect.DeepEqual(data.Decimals, []float64{1.5, 2.5}) {
		t.Fatalf("unexpected float slice: %#v", data.Decimals)
	}
	if len(data.Dates) != 2 || data.Dates[0].Format("2006-01-02") != "2026-04-01" || data.Dates[1].Format("2006-01-02") != "2026-04-02" {
		t.Fatalf("unexpected time slice: %#v", data.Dates)
	}
}

func TestGrabCustomConverters(t *testing.T) {
	type slug struct {
		Value string
	}

	type page struct {
		Slug  slug   `grab:".slug"`
		Slugs []slug `grab:".slugs li"`
	}

	grabber := New()
	grabber.RegisterConverter(reflect.TypeOf(slug{}), func(raw string) (any, error) {
		return slug{Value: strings.ToUpper(raw)}, nil
	})

	html := `<div class="slug">one</div><ul class="slugs"><li>two</li><li>three</li></ul>`
	var data page
	if err := grabber.GrabReader("https://example.com", strings.NewReader(html), &data); err != nil {
		t.Fatalf("grab custom types: %v", err)
	}

	if data.Slug.Value != "ONE" {
		t.Fatalf("unexpected custom scalar conversion: %#v", data.Slug)
	}
	want := []slug{{Value: "TWO"}, {Value: "THREE"}}
	if !reflect.DeepEqual(data.Slugs, want) {
		t.Fatalf("unexpected custom slice conversion: %#v", data.Slugs)
	}
}

func TestGrabSupportsSelectorFallbacks(t *testing.T) {
	type page struct {
		Headline string   `grab:".missing||h1||title"`
		Tags     []string `grab:".missing||.tags li"`
	}

	html := `<html><body><h1>Fallback Headline</h1><ul class="tags"><li>a</li><li>b</li></ul></body></html>`
	var data page
	if err := New().GrabReader("https://example.com", strings.NewReader(html), &data); err != nil {
		t.Fatalf("grab with selector fallbacks: %v", err)
	}

	if data.Headline != "Fallback Headline" {
		t.Fatalf("expected fallback headline, got %q", data.Headline)
	}
	if !reflect.DeepEqual(data.Tags, []string{"a", "b"}) {
		t.Fatalf("unexpected tags: %#v", data.Tags)
	}
}

func TestGrabSupportsOptionalDefaultsAndWhitespaceModes(t *testing.T) {
	type page struct {
		Missing   string `grab:".missing" optional:"true"`
		Fallback  string `grab:".missing" optional:"true" default:"fallback"`
		Filtered  string `grab:".value" filter:"^x$" optional:"true" default:"filtered"`
		Count     int    `grab:".missing-int" optional:"true"`
		Trimmed   string `grab:".text" whitespace:"trim"`
		Collapsed string `grab:".text" whitespace:"collapse"`
		Preserved string `grab:".text" whitespace:"preserve"`
	}

	html := `<div class="value">keep</div><div class="text">  hello
 world  </div>`
	var data page
	if err := New().GrabReader("https://example.com", strings.NewReader(html), &data); err != nil {
		t.Fatalf("grab optional/default/whitespace: %v", err)
	}

	if data.Missing != "" || data.Count != 0 {
		t.Fatalf("expected zero values for optional missing fields, got %#v", data)
	}
	if data.Fallback != "fallback" || data.Filtered != "filtered" {
		t.Fatalf("unexpected defaults: %#v", data)
	}
	if data.Trimmed != "hello\n world" {
		t.Fatalf("unexpected trimmed value: %q", data.Trimmed)
	}
	if data.Collapsed != "hello world" {
		t.Fatalf("unexpected collapsed value: %q", data.Collapsed)
	}
	if !strings.HasPrefix(data.Preserved, "  hello") || !strings.HasSuffix(data.Preserved, "  ") {
		t.Fatalf("expected preserved whitespace, got %q", data.Preserved)
	}
}

func TestGrabSupportsHTMLResolveAndSeparator(t *testing.T) {
	type page struct {
		Markup string `grab:".markup" html:"true" whitespace:"preserve"`
		Link   string `grab:"a[href]" attr:"href" resolve:"url"`
		Joined string `grab:".tag" sep:", "`
	}

	html := `<div class="markup"><span>Hello</span><b>World</b></div><a href="/posts/1">Read</a><span class="tag">go</span><span class="tag">web</span>`
	var data page
	if err := New().GrabReader("https://example.com/blog/", strings.NewReader(html), &data); err != nil {
		t.Fatalf("grab html/resolve/sep: %v", err)
	}

	if data.Markup != `<span>Hello</span><b>World</b>` {
		t.Fatalf("unexpected html extraction: %q", data.Markup)
	}
	if data.Link != "https://example.com/posts/1" {
		t.Fatalf("unexpected resolved link: %q", data.Link)
	}
	if data.Joined != "go, web" {
		t.Fatalf("unexpected joined scalar: %q", data.Joined)
	}
}

func TestGrabSupportsContextOnScalarSlices(t *testing.T) {
	type page struct {
		Prices []int `grab:".price" context:".item"`
	}

	html := `
		<div class="item"><span class="price">10</span></div>
		<div class="item"><span class="price">20</span></div>
		<div class="item"><span class="price">30</span></div>
	`
	var data page
	if err := New().GrabReader("https://example.com", strings.NewReader(html), &data); err != nil {
		t.Fatalf("grab context slice: %v", err)
	}

	if !reflect.DeepEqual(data.Prices, []int{10, 20, 30}) {
		t.Fatalf("unexpected context slice values: %#v", data.Prices)
	}
}

func TestGrabStructuredErrors(t *testing.T) {
	t.Run("field error", func(t *testing.T) {
		var data struct {
			Count int `grab:".count"`
		}

		err := New().GrabReader("https://example.com", strings.NewReader(`<div class="count">not-a-number</div>`), &data)
		var fieldErr *FieldError
		if !errors.As(err, &fieldErr) {
			t.Fatalf("expected FieldError, got %v", err)
		}
		if fieldErr.Field != "Count" {
			t.Fatalf("expected field path %q, got %q", "Count", fieldErr.Field)
		}
	})

	t.Run("status error", func(t *testing.T) {
		grabber := New()
		grabber.HTTPClient = &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return htmlResponse(req, http.StatusInternalServerError, `<html></html>`), nil
			}),
		}

		var data struct {
			Title string `grab:"title"`
		}
		err := grabber.Grab("http://status.test", &data)
		var statusErr *StatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("expected StatusError, got %v", err)
		}
	})

	t.Run("request error", func(t *testing.T) {
		sentinel := errors.New("request failure")
		grabber := New()
		grabber.BeforeRequest = func(req *http.Request) error {
			return sentinel
		}

		var data struct {
			Title string `grab:"title"`
		}
		err := grabber.Grab("http://request.test", &data)
		var requestErr *RequestError
		if !errors.As(err, &requestErr) {
			t.Fatalf("expected RequestError, got %v", err)
		}
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected sentinel to be wrapped, got %v", err)
		}
	})
}

func TestGrabRejectsHTMLAndAttrTogether(t *testing.T) {
	var data struct {
		Body string `grab:".body" html:"true" attr:"href"`
	}

	err := New().GrabReader("https://example.com", strings.NewReader(`<div class="body">ignored</div>`), &data)
	var fieldErr *FieldError
	if !errors.As(err, &fieldErr) {
		t.Fatalf("expected FieldError, got %v", err)
	}
	if !strings.Contains(fieldErr.Error(), "html and attr") {
		t.Fatalf("expected html/attr conflict, got %v", err)
	}
}
