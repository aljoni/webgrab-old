package webgrab

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Tag names for struct fields.
const (
	tagNameGrab       = "grab"
	tagNameExtract    = "extract"
	tagNameFilter     = "filter"
	tagNameAttribute  = "attr"
	tagNameContext    = "context"
	tagNameOptional   = "optional"
	tagNameDefault    = "default"
	tagNameWhitespace = "whitespace"
	tagNameHTML       = "html"
	tagNameResolve    = "resolve"
	tagNameLayout     = "layout"
	tagNameSeparator  = "sep"
)

const (
	whitespaceTrim     = "trim"
	whitespaceCollapse = "collapse"
	whitespacePreserve = "preserve"
)

var (
	timeType    = reflect.TypeOf(time.Time{})
	schemaCache sync.Map
)

// ConverterFunc converts a processed raw string into a custom destination value.
type ConverterFunc func(raw string) (any, error)

// FieldError describes a field-level scraping or conversion failure.
type FieldError struct {
	Field    string
	Selector string
	Context  string
	Cause    error
}

func (e *FieldError) Error() string {
	parts := []string{fmt.Sprintf("field %s", e.Field)}
	if e.Selector != "" {
		parts = append(parts, fmt.Sprintf("selector %q", e.Selector))
	}
	if e.Context != "" {
		parts = append(parts, fmt.Sprintf("context %q", e.Context))
	}
	if e.Cause != nil {
		parts = append(parts, e.Cause.Error())
	}
	return strings.Join(parts, ": ")
}

func (e *FieldError) Unwrap() error {
	return e.Cause
}

// StatusError reports a disallowed HTTP status code.
type StatusError struct {
	URL        string
	StatusCode int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected status code %d for %s", e.StatusCode, e.URL)
}

// RequestError reports a request lifecycle failure.
type RequestError struct {
	URL   string
	Cause error
}

func (e *RequestError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("request failed for %s", e.URL)
	}
	return fmt.Sprintf("request failed for %s: %v", e.URL, e.Cause)
}

func (e *RequestError) Unwrap() error {
	return e.Cause
}

// Grabber configures and performs web scraping.
type Grabber struct {
	Timeout            int
	MaxRedirects       int
	StrictMode         bool
	RespectRobotsTxt   bool
	UserAgent          string
	HTTPClient         *http.Client
	BeforeRequest      func(*http.Request) error
	AfterResponse      func(*http.Response) error
	AllowedStatusCodes []int
	RetryCount         int
	RetryDelay         time.Duration
	RetryBackoff       float64

	state *grabberState
}

type grabberState struct {
	mu         sync.RWMutex
	converters map[reflect.Type]ConverterFunc
	robots     map[string]*robotsPolicy
}

type robotsPolicy struct {
	groups map[string][]robotsRule
}

type robotsRule struct {
	pattern string
	allow   bool
}

type compiledSchema struct {
	fields []compiledField
}

type compiledField struct {
	name          string
	index         int
	fieldType     reflect.Type
	selectors     []string
	context       string
	attribute     string
	filter        *regexp.Regexp
	filterSource  string
	extract       *regexp.Regexp
	extractSource string
	optional      bool
	defaultValue  string
	whitespace    string
	html          bool
	resolve       string
	layout        string
	separator     string
	configured    bool
	compileErr    error
}

type collectedValues struct {
	values         []string
	selector       string
	hadAnyMatch    bool
	contextMissing bool
}

// RegisterConverter registers a converter for an exact destination type.
func (g *Grabber) RegisterConverter(target reflect.Type, fn ConverterFunc) {
	if target == nil || fn == nil {
		return
	}
	state := g.ensureState()
	state.mu.Lock()
	defer state.mu.Unlock()
	state.converters[target] = fn
}

func (g *Grabber) ensureState() *grabberState {
	if g.state == nil {
		g.state = &grabberState{
			converters: make(map[reflect.Type]ConverterFunc),
			robots:     make(map[string]*robotsPolicy),
		}
	}
	return g.state
}

func (g *Grabber) lookupConverter(target reflect.Type) (ConverterFunc, bool) {
	if g == nil || g.state == nil {
		return nil, false
	}
	g.state.mu.RLock()
	defer g.state.mu.RUnlock()
	fn, ok := g.state.converters[target]
	return fn, ok
}

func (f compiledField) selectorDescription() string {
	return strings.Join(f.selectors, "||")
}

func (f compiledField) usesScalarExtraction() bool {
	return len(f.selectors) > 0 || f.attribute != "" || f.extract != nil || f.filter != nil || f.html || f.resolve != "" || f.layout != "" || f.separator != "" || f.defaultValue != ""
}

func (f compiledField) configuredForNestedStruct() bool {
	return f.context != "" || f.optional
}

func validateDestination(data any) (reflect.Value, error) {
	if data == nil {
		return reflect.Value{}, fmt.Errorf("data must be a non-nil pointer to a struct")
	}

	value := reflect.ValueOf(data)
	if value.Kind() != reflect.Ptr || value.IsNil() {
		return reflect.Value{}, fmt.Errorf("data must be a non-nil pointer to a struct")
	}

	elem := value.Elem()
	if elem.Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("data must be a pointer to a struct")
	}

	return elem, nil
}

// Grab fetches and populates data from a URL into the provided struct pointer.
func (g *Grabber) Grab(rawURL string, data any) error {
	if _, err := validateDestination(data); err != nil {
		return err
	}

	baseURL, doc, err := g.fetchDocument(rawURL)
	if err != nil {
		return err
	}

	return g.scrapeDocument(baseURL, doc, data)
}

// GrabReader populates data from an existing reader and optional base URL.
func (g *Grabber) GrabReader(baseURL string, r io.Reader, data any) error {
	if _, err := validateDestination(data); err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("reader must not be nil")
	}

	parsedBaseURL, err := parseBaseURL(baseURL)
	if err != nil {
		return err
	}

	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return err
	}

	return g.scrapeDocument(parsedBaseURL, doc, data)
}

// GrabDocument populates data from an existing goquery document and optional base URL.
func (g *Grabber) GrabDocument(baseURL string, doc *goquery.Document, data any) error {
	if _, err := validateDestination(data); err != nil {
		return err
	}
	if doc == nil {
		return fmt.Errorf("document must not be nil")
	}

	parsedBaseURL, err := parseBaseURL(baseURL)
	if err != nil {
		return err
	}

	return g.scrapeDocument(parsedBaseURL, doc, data)
}

func (g *Grabber) scrapeDocument(baseURL *url.URL, doc *goquery.Document, data any) error {
	if doc == nil {
		return fmt.Errorf("document must not be nil")
	}
	return g.scrapeSelection(baseURL, doc.Selection, data, "")
}

// scrapeStruct performs the actual scraping for a nested struct.
func (g *Grabber) scrapeStruct(doc *goquery.Document, nested any) error {
	return g.scrapeDocument(nil, doc, nested)
}

func (g *Grabber) fetchDocument(rawURL string) (*url.URL, *goquery.Document, error) {
	client := g.httpClient()

	if g != nil && g.RespectRobotsTxt {
		if err := g.checkRobotsPolicy(client, rawURL); err != nil {
			return nil, nil, err
		}
	}

	retries := g.retryCount()

	for attempt := 0; attempt <= retries; attempt++ {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, nil, &RequestError{URL: rawURL, Cause: err}
		}

		if userAgent := g.userAgent(); userAgent != "" {
			req.Header.Set("User-Agent", userAgent)
		}

		if g.BeforeRequest != nil {
			if err := g.BeforeRequest(req); err != nil {
				return nil, nil, &RequestError{URL: rawURL, Cause: err}
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			if attempt < retries {
				g.sleepBeforeRetry(attempt)
				continue
			}
			return nil, nil, &RequestError{URL: rawURL, Cause: err}
		}

		finalURL := rawURL
		if resp.Request != nil && resp.Request.URL != nil {
			finalURL = resp.Request.URL.String()
		}

		if g.AfterResponse != nil {
			if err := g.AfterResponse(resp); err != nil {
				resp.Body.Close()
				return nil, nil, &RequestError{URL: finalURL, Cause: err}
			}
		}

		if !g.isAllowedStatus(resp.StatusCode) {
			if attempt < retries && isRetryableStatus(resp.StatusCode) {
				resp.Body.Close()
				g.sleepBeforeRetry(attempt)
				continue
			}
			resp.Body.Close()
			return nil, nil, &StatusError{URL: finalURL, StatusCode: resp.StatusCode}
		}

		doc, err := goquery.NewDocumentFromReader(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, nil, err
		}

		parsedURL, err := url.Parse(finalURL)
		if err != nil {
			return nil, nil, &RequestError{URL: finalURL, Cause: err}
		}
		return parsedURL, doc, nil
	}

	return nil, nil, &RequestError{URL: rawURL, Cause: fmt.Errorf("request retries exhausted")}
}

func (g *Grabber) scrapeSelection(baseURL *url.URL, root *goquery.Selection, data any, path string) error {
	if root == nil {
		return fmt.Errorf("selection must not be nil")
	}

	dest, err := validateDestination(data)
	if err != nil {
		return err
	}

	schema, err := getSchema(dest.Type())
	if err != nil {
		return err
	}

	var errs []error
	for _, field := range schema.fields {
		fieldPath := joinFieldPath(path, field.name)
		value := dest.Field(field.index)

		if !value.CanSet() {
			if field.configured {
				errs = append(errs, wrapFieldError(fieldPath, field, fmt.Errorf("field is not settable; use an exported field")))
			}
			continue
		}

		if field.compileErr != nil {
			errs = append(errs, wrapFieldError(fieldPath, field, field.compileErr))
			continue
		}

		if !field.configured && !g.shouldHandleUntypedField(value.Type()) {
			continue
		}

		if err := g.applyField(baseURL, root, value, field, fieldPath); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (g *Grabber) applyField(baseURL *url.URL, root *goquery.Selection, value reflect.Value, field compiledField, path string) error {
	switch {
	case g.isNestedStructField(value.Type()):
		return g.applyNestedStructField(baseURL, root, value, field, path)
	case g.isStructSliceField(value.Type()):
		return g.applyStructSliceField(baseURL, root, value, field, path)
	case g.isScalarSliceField(value.Type()):
		return g.applyScalarSliceField(baseURL, root, value, field, path)
	default:
		return g.applyScalarField(baseURL, root, value, field, path)
	}
}

func (g *Grabber) applyNestedStructField(baseURL *url.URL, root *goquery.Selection, value reflect.Value, field compiledField, path string) error {
	if field.usesScalarExtraction() {
		return wrapFieldError(path, field, fmt.Errorf("nested struct fields only support context and optional tags"))
	}

	targetRoot := root
	if field.context != "" {
		contexts := root.Find(field.context)
		if contexts.Length() == 0 {
			if field.optional {
				return nil
			}
			return wrapFieldError(path, field, fmt.Errorf("context not found"))
		}
		targetRoot = contexts.First()
	}

	return g.scrapeSelection(baseURL, targetRoot, value.Addr().Interface(), path)
}

func (g *Grabber) applyStructSliceField(baseURL *url.URL, root *goquery.Selection, value reflect.Value, field compiledField, path string) error {
	if field.context == "" {
		if field.optional {
			return nil
		}
		return wrapFieldError(path, field, fmt.Errorf("slice of structs requires a context tag"))
	}
	if field.usesScalarExtraction() && (len(field.selectors) > 0 || field.attribute != "" || field.extract != nil || field.filter != nil || field.html || field.resolve != "" || field.layout != "" || field.separator != "" || field.defaultValue != "") {
		return wrapFieldError(path, field, fmt.Errorf("slice of structs only supports context and optional tags"))
	}

	contexts := root.Find(field.context)
	if contexts.Length() == 0 {
		if field.optional {
			return nil
		}
		return wrapFieldError(path, field, fmt.Errorf("context not found"))
	}

	slice := reflect.MakeSlice(value.Type(), 0, contexts.Length())
	var errs []error

	contexts.Each(func(i int, ctx *goquery.Selection) {
		elemPtr := reflect.New(value.Type().Elem())
		itemPath := fmt.Sprintf("%s[%d]", path, i)
		if err := g.scrapeSelection(baseURL, ctx, elemPtr.Interface(), itemPath); err != nil {
			if g.StrictMode {
				errs = append(errs, err)
			}
			return
		}
		slice = reflect.Append(slice, elemPtr.Elem())
	})

	value.Set(slice)
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (g *Grabber) applyScalarField(baseURL *url.URL, root *goquery.Selection, value reflect.Value, field compiledField, path string) error {
	if !field.configured {
		return nil
	}

	collected, err := g.collectScalarValues(baseURL, root, field, false)
	if err != nil {
		return wrapFieldError(path, field, err)
	}

	raw, hasValue, err := g.scalarRawValue(field, collected)
	if err != nil {
		return wrapFieldError(path, field, err)
	}
	if !hasValue {
		return nil
	}

	converted, err := g.convertValue(raw, value.Type(), field)
	if err != nil {
		return wrapFieldError(path, field, err)
	}

	value.Set(converted)
	return nil
}

func (g *Grabber) applyScalarSliceField(baseURL *url.URL, root *goquery.Selection, value reflect.Value, field compiledField, path string) error {
	if !field.configured {
		return nil
	}

	collected, err := g.collectScalarValues(baseURL, root, field, true)
	if err != nil {
		return wrapFieldError(path, field, err)
	}

	targetType := value.Type().Elem()
	values, errs := g.convertSliceValues(collected.values, targetType, field, path)
	if len(values) == 0 && field.optional && field.defaultValue != "" {
		values, errs = g.convertSliceValues([]string{field.defaultValue}, targetType, field, path)
	}

	if len(values) == 0 && !field.optional && len(errs) == 0 {
		return wrapFieldError(path, field, fmt.Errorf("no values found"))
	}

	slice := reflect.MakeSlice(value.Type(), 0, len(values))
	for _, item := range values {
		slice = reflect.Append(slice, item)
	}
	value.Set(slice)

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (g *Grabber) convertSliceValues(rawValues []string, targetType reflect.Type, field compiledField, path string) ([]reflect.Value, []error) {
	values := make([]reflect.Value, 0, len(rawValues))
	var errs []error
	for i, raw := range rawValues {
		converted, err := g.convertValue(raw, targetType, field)
		if err != nil {
			itemField := fmt.Sprintf("%s[%d]", path, i)
			errs = append(errs, wrapFieldError(itemField, field, err))
			continue
		}
		values = append(values, converted)
	}
	return values, errs
}

func (g *Grabber) scalarRawValue(field compiledField, collected collectedValues) (string, bool, error) {
	if len(collected.values) == 0 {
		if field.optional {
			if field.defaultValue != "" {
				return field.defaultValue, true, nil
			}
			return "", false, nil
		}
		if collected.contextMissing && field.context != "" {
			return "", false, fmt.Errorf("context not found")
		}
		if field.selectorDescription() == "" {
			return "", false, fmt.Errorf("no selector configured")
		}
		return "", false, fmt.Errorf("no values found")
	}

	if field.separator != "" {
		return strings.Join(collected.values, field.separator), true, nil
	}
	return collected.values[0], true, nil
}

func (g *Grabber) collectScalarValues(baseURL *url.URL, root *goquery.Selection, field compiledField, collectAllContexts bool) (collectedValues, error) {
	var result collectedValues

	contexts, contextMissing := selectContexts(root, field.context, collectAllContexts)
	if contextMissing {
		result.contextMissing = true
		return result, nil
	}

	for _, ctx := range contexts {
		values, selector, matchedAny, err := g.collectFromContext(baseURL, ctx, field)
		if err != nil {
			return result, err
		}
		if selector != "" {
			result.selector = selector
		}
		if matchedAny {
			result.hadAnyMatch = true
		}
		result.values = append(result.values, values...)

		if !collectAllContexts {
			break
		}
	}

	return result, nil
}

func (g *Grabber) collectFromContext(baseURL *url.URL, ctx *goquery.Selection, field compiledField) ([]string, string, bool, error) {
	if ctx == nil {
		return nil, "", false, nil
	}
	if len(field.selectors) == 0 {
		return nil, "", false, nil
	}

	for _, selector := range field.selectors {
		matches := ctx.Find(selector)
		if matches.Length() == 0 {
			continue
		}

		values := make([]string, 0, matches.Length())
		var errs []error
		matches.Each(func(i int, match *goquery.Selection) {
			raw, include, err := g.processSelectionValue(baseURL, match, field)
			if err != nil {
				errs = append(errs, fmt.Errorf("match %d: %w", i, err))
				return
			}
			if include {
				values = append(values, raw)
			}
		})

		if len(errs) > 0 {
			return values, selector, true, errors.Join(errs...)
		}
		return values, selector, true, nil
	}

	return nil, "", false, nil
}

func (g *Grabber) processSelectionValue(baseURL *url.URL, match *goquery.Selection, field compiledField) (string, bool, error) {
	raw, err := extractSelectionValue(match, field)
	if err != nil {
		return "", false, err
	}

	raw = normalizeWhitespace(raw, field.whitespace)
	if field.filter != nil && !field.filter.MatchString(raw) {
		return "", false, nil
	}

	if field.extract != nil {
		capture := field.extract.FindStringSubmatch(raw)
		if len(capture) < 2 {
			return "", false, fmt.Errorf("extract regex %q did not match", field.extractSource)
		}
		raw = capture[1]
	}

	if field.resolve == "url" {
		if raw == "" {
			return "", true, nil
		}
		if baseURL == nil {
			return "", false, fmt.Errorf("resolve tag requires a base URL")
		}
		ref, err := url.Parse(raw)
		if err != nil {
			return "", false, fmt.Errorf("resolve URL %q: %w", raw, err)
		}
		raw = baseURL.ResolveReference(ref).String()
	}

	return raw, true, nil
}

func extractSelectionValue(match *goquery.Selection, field compiledField) (string, error) {
	switch {
	case field.html:
		html, err := match.Html()
		if err != nil {
			return "", err
		}
		return html, nil
	case field.attribute != "":
		return match.AttrOr(field.attribute, ""), nil
	default:
		return match.Text(), nil
	}
}

func normalizeWhitespace(raw, mode string) string {
	switch mode {
	case whitespaceCollapse:
		return strings.Join(strings.Fields(raw), " ")
	case whitespacePreserve:
		return raw
	default:
		return strings.TrimSpace(raw)
	}
}

func (g *Grabber) convertValue(raw string, target reflect.Type, field compiledField) (reflect.Value, error) {
	if converter, ok := g.lookupConverter(target); ok {
		converted, err := converter(raw)
		if err != nil {
			return reflect.Value{}, err
		}
		value := reflect.ValueOf(converted)
		if !value.IsValid() {
			return reflect.Value{}, fmt.Errorf("converter returned nil")
		}
		if value.Type().AssignableTo(target) {
			return value, nil
		}
		if value.Type().ConvertibleTo(target) {
			return value.Convert(target), nil
		}
		return reflect.Value{}, fmt.Errorf("converter returned %s, cannot assign to %s", value.Type(), target)
	}

	if target == timeType {
		layout := field.layout
		if layout == "" {
			layout = time.RFC3339
		}
		parsed, err := time.Parse(layout, raw)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(parsed), nil
	}

	switch target.Kind() {
	case reflect.String:
		value := reflect.New(target).Elem()
		value.SetString(raw)
		return value, nil
	case reflect.Bool:
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return reflect.Value{}, err
		}
		value := reflect.New(target).Elem()
		value.SetBool(parsed)
		return value, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(raw, 10, target.Bits())
		if err != nil {
			return reflect.Value{}, err
		}
		value := reflect.New(target).Elem()
		value.SetInt(parsed)
		return value, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		parsed, err := strconv.ParseUint(raw, 10, target.Bits())
		if err != nil {
			return reflect.Value{}, err
		}
		value := reflect.New(target).Elem()
		value.SetUint(parsed)
		return value, nil
	case reflect.Float32, reflect.Float64:
		parsed, err := strconv.ParseFloat(raw, target.Bits())
		if err != nil {
			return reflect.Value{}, err
		}
		value := reflect.New(target).Elem()
		value.SetFloat(parsed)
		return value, nil
	default:
		return reflect.Value{}, fmt.Errorf("unsupported field type %s", target)
	}
}

func (g *Grabber) isNestedStructField(target reflect.Type) bool {
	return target.Kind() == reflect.Struct && target != timeType && !g.hasCustomConverter(target)
}

func (g *Grabber) isStructSliceField(target reflect.Type) bool {
	if target.Kind() != reflect.Slice {
		return false
	}
	elem := target.Elem()
	return elem.Kind() == reflect.Struct && elem != timeType && !g.hasCustomConverter(elem)
}

func (g *Grabber) isScalarSliceField(target reflect.Type) bool {
	return target.Kind() == reflect.Slice && !g.isStructSliceField(target)
}

func (g *Grabber) shouldHandleUntypedField(target reflect.Type) bool {
	return g.isNestedStructField(target)
}

func (g *Grabber) hasCustomConverter(target reflect.Type) bool {
	_, ok := g.lookupConverter(target)
	return ok
}

func (g *Grabber) httpClient() *http.Client {
	if g != nil && g.HTTPClient != nil {
		return g.HTTPClient
	}

	timeout := 10 * time.Second
	if g != nil && g.Timeout > 0 {
		timeout = time.Duration(g.Timeout) * time.Second
	}

	maxRedirects := 10
	if g != nil && g.MaxRedirects > 0 {
		maxRedirects = g.MaxRedirects
	}

	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

func (g *Grabber) retryCount() int {
	if g == nil || g.RetryCount < 0 {
		return 0
	}
	return g.RetryCount
}

func (g *Grabber) userAgent() string {
	if g != nil && g.UserAgent != "" {
		return g.UserAgent
	}
	return "Mozilla/5.0 (compatible; WebGrab/1.0;) Go"
}

func (g *Grabber) isAllowedStatus(status int) bool {
	statuses := []int{http.StatusOK}
	if g != nil && len(g.AllowedStatusCodes) > 0 {
		statuses = g.AllowedStatusCodes
	}
	for _, allowed := range statuses {
		if status == allowed {
			return true
		}
	}
	return false
}

func isRetryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func (g *Grabber) sleepBeforeRetry(attempt int) {
	if g == nil || g.RetryDelay <= 0 {
		return
	}

	backoff := 1.0
	if g.RetryBackoff > 0 {
		backoff = g.RetryBackoff
	}

	delay := float64(g.RetryDelay)
	if attempt > 0 {
		delay *= math.Pow(backoff, float64(attempt))
	}
	time.Sleep(time.Duration(delay))
}

func (g *Grabber) checkRobotsPolicy(client *http.Client, rawURL string) error {
	targetURL, err := url.Parse(rawURL)
	if err != nil {
		return &RequestError{URL: rawURL, Cause: err}
	}
	if targetURL.Scheme == "" || targetURL.Host == "" {
		return &RequestError{URL: rawURL, Cause: fmt.Errorf("robots.txt checks require an absolute URL")}
	}

	origin := targetURL.Scheme + "://" + targetURL.Host
	policy, err := g.robotsPolicy(client, origin)
	if err != nil {
		return err
	}
	if policy == nil {
		return nil
	}

	if !policy.allowed(g.robotsAgent(), robotsTargetPath(targetURL)) {
		return &RequestError{
			URL:   rawURL,
			Cause: fmt.Errorf("blocked by robots.txt"),
		}
	}
	return nil
}

func (g *Grabber) robotsPolicy(client *http.Client, origin string) (*robotsPolicy, error) {
	state := g.ensureState()

	state.mu.RLock()
	cached, ok := state.robots[origin]
	state.mu.RUnlock()
	if ok {
		return cached, nil
	}

	req, err := http.NewRequest(http.MethodGet, origin+"/robots.txt", nil)
	if err != nil {
		return nil, &RequestError{URL: origin + "/robots.txt", Cause: err}
	}
	if userAgent := g.userAgent(); userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, &RequestError{URL: origin + "/robots.txt", Cause: err}
	}
	defer resp.Body.Close()

	policy, err := parseRobotsResponse(resp)
	if err != nil {
		return nil, &RequestError{URL: origin + "/robots.txt", Cause: err}
	}

	state.mu.Lock()
	if existing, ok := state.robots[origin]; ok {
		state.mu.Unlock()
		return existing, nil
	}
	state.robots[origin] = policy
	state.mu.Unlock()

	return policy, nil
}

func parseRobotsResponse(resp *http.Response) (*robotsPolicy, error) {
	switch resp.StatusCode {
	case http.StatusOK:
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return parseRobotsTxt(string(body)), nil
	case http.StatusNotFound, http.StatusGone:
		return nil, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return &robotsPolicy{
			groups: map[string][]robotsRule{
				"*": {{pattern: "/", allow: false}},
			},
		}, nil
	default:
		return nil, fmt.Errorf("robots.txt returned status %d", resp.StatusCode)
	}
}

func parseRobotsTxt(content string) *robotsPolicy {
	policy := &robotsPolicy{
		groups: make(map[string][]robotsRule),
	}

	var currentAgents []string
	hadRule := false

	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(stripRobotsComment(rawLine))
		if line == "" {
			currentAgents = nil
			hadRule = false
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.ToLower(strings.TrimSpace(parts[1]))

		switch key {
		case "user-agent":
			if hadRule {
				currentAgents = nil
				hadRule = false
			}
			if value == "" {
				continue
			}
			currentAgents = append(currentAgents, value)
			if _, ok := policy.groups[value]; !ok {
				policy.groups[value] = nil
			}
		case "allow", "disallow":
			if len(currentAgents) == 0 {
				continue
			}
			hadRule = true
			for _, agent := range currentAgents {
				policy.groups[agent] = append(policy.groups[agent], robotsRule{
					pattern: value,
					allow:   key == "allow",
				})
			}
		}
	}

	return policy
}

func stripRobotsComment(line string) string {
	if idx := strings.Index(line, "#"); idx >= 0 {
		return line[:idx]
	}
	return line
}

func (p *robotsPolicy) allowed(agent, target string) bool {
	if p == nil {
		return true
	}

	rules := p.rulesForAgent(strings.ToLower(agent))
	if len(rules) == 0 {
		return true
	}

	bestLen := -1
	bestAllow := true
	for _, rule := range rules {
		if rule.pattern == "" && !rule.allow {
			continue
		}
		if !robotsRuleMatches(rule.pattern, target) {
			continue
		}
		length := len(rule.pattern)
		if length > bestLen || (length == bestLen && rule.allow) {
			bestLen = length
			bestAllow = rule.allow
		}
	}

	if bestLen == -1 {
		return true
	}
	return bestAllow
}

func (p *robotsPolicy) rulesForAgent(agent string) []robotsRule {
	bestAgent := ""
	var rules []robotsRule
	for candidate, candidateRules := range p.groups {
		if candidate == "*" {
			if bestAgent == "" {
				bestAgent = candidate
				rules = candidateRules
			}
			continue
		}
		if strings.HasPrefix(agent, candidate) && len(candidate) > len(bestAgent) {
			bestAgent = candidate
			rules = candidateRules
		}
	}
	return rules
}

func robotsRuleMatches(pattern, target string) bool {
	if pattern == "" {
		return true
	}

	endAnchored := strings.HasSuffix(pattern, "$")
	if endAnchored {
		pattern = strings.TrimSuffix(pattern, "$")
	}

	parts := strings.Split(pattern, "*")
	position := 0
	startAnchored := !strings.HasPrefix(pattern, "*")
	for i, part := range parts {
		if part == "" {
			continue
		}
		index := strings.Index(target[position:], part)
		if index < 0 {
			return false
		}
		if i == 0 && startAnchored && position+index != 0 {
			return false
		}
		position += index + len(part)
	}

	if endAnchored && !strings.HasSuffix(target, strings.TrimSuffix(pattern, "*")) {
		last := parts[len(parts)-1]
		return last == "" || strings.HasSuffix(target, last)
	}

	return true
}

func robotsTargetPath(u *url.URL) string {
	target := u.EscapedPath()
	if target == "" {
		target = "/"
	}
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	return target
}

func (g *Grabber) robotsAgent() string {
	userAgent := strings.ToLower(g.userAgent())
	if strings.Contains(userAgent, "webgrab") {
		return "webgrab"
	}

	for _, token := range strings.Fields(userAgent) {
		product := token
		if slash := strings.Index(product, "/"); slash >= 0 {
			product = product[:slash]
		}
		product = strings.Trim(product, "();")
		if product != "" {
			return product
		}
	}

	return "*"
}

func parseBaseURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, &RequestError{URL: raw, Cause: err}
	}
	return parsed, nil
}

func selectContexts(root *goquery.Selection, selector string, all bool) ([]*goquery.Selection, bool) {
	if selector == "" {
		return []*goquery.Selection{root}, false
	}

	matches := root.Find(selector)
	if matches.Length() == 0 {
		return nil, true
	}

	if !all {
		return []*goquery.Selection{matches.First()}, false
	}

	contexts := make([]*goquery.Selection, 0, matches.Length())
	matches.Each(func(_ int, selection *goquery.Selection) {
		contexts = append(contexts, selection)
	})
	return contexts, false
}

func joinFieldPath(prefix, field string) string {
	if prefix == "" {
		return field
	}
	return prefix + "." + field
}

func wrapFieldError(path string, field compiledField, cause error) error {
	return &FieldError{
		Field:    path,
		Selector: field.selectorDescription(),
		Context:  field.context,
		Cause:    cause,
	}
}

func getSchema(target reflect.Type) (*compiledSchema, error) {
	if cached, ok := schemaCache.Load(target); ok {
		return cached.(*compiledSchema), nil
	}

	schema := compileSchema(target)
	actual, _ := schemaCache.LoadOrStore(target, schema)
	return actual.(*compiledSchema), nil
}

func compileSchema(target reflect.Type) *compiledSchema {
	schema := &compiledSchema{
		fields: make([]compiledField, 0, target.NumField()),
	}

	for i := 0; i < target.NumField(); i++ {
		structField := target.Field(i)
		field := compiledField{
			name:         structField.Name,
			index:        i,
			fieldType:    structField.Type,
			context:      strings.TrimSpace(structField.Tag.Get(tagNameContext)),
			attribute:    strings.TrimSpace(structField.Tag.Get(tagNameAttribute)),
			defaultValue: structField.Tag.Get(tagNameDefault),
			resolve:      strings.TrimSpace(structField.Tag.Get(tagNameResolve)),
			layout:       structField.Tag.Get(tagNameLayout),
			separator:    structField.Tag.Get(tagNameSeparator),
			whitespace:   whitespaceTrim,
		}

		rawSelector := structField.Tag.Get(tagNameGrab)
		if rawSelector != "" {
			field.selectors = parseSelectorFallbacks(rawSelector)
		}

		field.configured = rawSelector != "" ||
			structField.Tag.Get(tagNameExtract) != "" ||
			structField.Tag.Get(tagNameFilter) != "" ||
			field.attribute != "" ||
			field.context != "" ||
			structField.Tag.Get(tagNameOptional) != "" ||
			structField.Tag.Get(tagNameDefault) != "" ||
			structField.Tag.Get(tagNameWhitespace) != "" ||
			structField.Tag.Get(tagNameHTML) != "" ||
			field.resolve != "" ||
			field.layout != "" ||
			field.separator != ""

		if rawSelector != "" && len(field.selectors) == 0 {
			field.compileErr = fmt.Errorf("grab tag must contain at least one selector")
		}

		if optionalRaw := structField.Tag.Get(tagNameOptional); optionalRaw != "" {
			optional, err := strconv.ParseBool(optionalRaw)
			if err != nil {
				field.compileErr = firstError(field.compileErr, fmt.Errorf("optional tag must be a boolean: %w", err))
			} else {
				field.optional = optional
			}
		}

		if whitespaceRaw := strings.TrimSpace(structField.Tag.Get(tagNameWhitespace)); whitespaceRaw != "" {
			switch whitespaceRaw {
			case whitespaceTrim, whitespaceCollapse, whitespacePreserve:
				field.whitespace = whitespaceRaw
			default:
				field.compileErr = firstError(field.compileErr, fmt.Errorf("invalid whitespace mode %q", whitespaceRaw))
			}
		}

		if htmlRaw := structField.Tag.Get(tagNameHTML); htmlRaw != "" {
			html, err := strconv.ParseBool(htmlRaw)
			if err != nil {
				field.compileErr = firstError(field.compileErr, fmt.Errorf("html tag must be a boolean: %w", err))
			} else {
				field.html = html
			}
		}

		if field.html && field.attribute != "" {
			field.compileErr = firstError(field.compileErr, fmt.Errorf("html and attr tags cannot be used together"))
		}

		if field.resolve != "" && field.resolve != "url" {
			field.compileErr = firstError(field.compileErr, fmt.Errorf("unsupported resolve mode %q", field.resolve))
		}

		if filterRaw := structField.Tag.Get(tagNameFilter); filterRaw != "" {
			filter, err := regexp.Compile(filterRaw)
			if err != nil {
				field.compileErr = firstError(field.compileErr, fmt.Errorf("invalid filter regex %q: %w", filterRaw, err))
			} else {
				field.filter = filter
				field.filterSource = filterRaw
			}
		}

		if extractRaw := structField.Tag.Get(tagNameExtract); extractRaw != "" {
			extract, err := regexp.Compile(extractRaw)
			if err != nil {
				field.compileErr = firstError(field.compileErr, fmt.Errorf("invalid extract regex %q: %w", extractRaw, err))
			} else {
				field.extract = extract
				field.extractSource = extractRaw
			}
		}

		schema.fields = append(schema.fields, field)
	}

	return schema
}

func parseSelectorFallbacks(raw string) []string {
	parts := strings.Split(raw, "||")
	selectors := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		selectors = append(selectors, part)
	}
	return selectors
}

func firstError(current, next error) error {
	if current != nil {
		return current
	}
	return next
}

// New returns a new Grabber with default values.
func New() *Grabber {
	return &Grabber{
		Timeout:      10,
		MaxRedirects: 10,
		StrictMode:   false,
		UserAgent:    "Mozilla/5.0 (compatible; WebGrab/1.0;) Go",
		RetryBackoff: 1.0,
		state: &grabberState{
			converters: make(map[reflect.Type]ConverterFunc),
			robots:     make(map[string]*robotsPolicy),
		},
	}
}
