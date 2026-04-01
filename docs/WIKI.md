# WebGrab Wiki

This document is a comprehensive guide to the current WebGrab feature set.
It covers the core scraping model, all supported tags, typed conversion,
custom converters, transport configuration, error handling, and practical
examples.

## Contents

1. [Overview](#overview)
2. [Installation](#installation)
3. [Quick Start](#quick-start)
4. [Core Model](#core-model)
5. [Scraping Entry Points](#scraping-entry-points)
6. [Grabber Configuration](#grabber-configuration)
7. [Struct Tags](#struct-tags)
8. [Supported Field Types](#supported-field-types)
9. [Custom Type Converters](#custom-type-converters)
10. [Nested Structs and Contexts](#nested-structs-and-contexts)
11. [Slices and Aggregation](#slices-and-aggregation)
12. [URL Resolution](#url-resolution)
13. [Whitespace and HTML Extraction](#whitespace-and-html-extraction)
14. [Filtering and Regex Extraction](#filtering-and-regex-extraction)
15. [HTTP Behavior, Hooks, and Retries](#http-behavior-hooks-and-retries)
16. [Strict Mode](#strict-mode)
17. [Errors](#errors)
18. [Patterns and Recipes](#patterns-and-recipes)
19. [Defaults and Behavior Notes](#defaults-and-behavior-notes)

## Overview

WebGrab is a tag-driven scraping library built on top of `goquery`.

You describe what to extract by defining a struct:

- where to find values with `grab:"..."`
- how to read them with tags like `attr`, `html`, `extract`, and `filter`
- how to shape them with types like `int`, `bool`, `time.Time`, slices, or
  custom converted types

WebGrab then populates your struct from:

- a URL with `Grab`
- an `io.Reader` with `GrabReader`
- an existing `*goquery.Document` with `GrabDocument`

## Installation

```bash
go get github.com/aljoni/webgrab
```

## Quick Start

```go
package main

import (
	"fmt"
	"time"

	"github.com/aljoni/webgrab"
)

type Article struct {
	Title     string    `grab:"h1||title"`
	Author    string    `grab:".author" optional:"true" default:"Unknown"`
	Published time.Time `grab:"time" attr:"datetime"`
	Tags      []string  `grab:".tags a"`
}

func main() {
	grabber := webgrab.New()

	var article Article
	if err := grabber.Grab("https://example.com/post", &article); err != nil {
		panic(err)
	}

	fmt.Println(article.Title)
	fmt.Println(article.Author)
	fmt.Println(article.Published)
	fmt.Println(article.Tags)
}
```

## Core Model

The library works in this order:

1. Find a field's selector.
2. Collect matching elements.
3. Read text, HTML, or an attribute.
4. Normalize whitespace.
5. Apply `filter`.
6. Apply `extract`.
7. Apply optional defaults.
8. Resolve relative URLs if requested.
9. Convert the final string into the destination field type.

This means tags compose naturally. For example:

```go
type Page struct {
	ID int `grab:"a[href]" attr:"href" extract:"id=(\\d+)"`
}
```

That field:

- selects `a[href]`
- reads the `href`
- extracts the first `id=` capture
- converts the result to `int`

## Scraping Entry Points

### `Grab`

Use `Grab` to fetch a URL and scrape its HTML:

```go
var page struct {
	Title string `grab:"title"`
}

err := grabber.Grab("https://example.com", &page)
```

### `GrabReader`

Use `GrabReader` when you already have HTML and optionally a base URL:

```go
html := strings.NewReader(`<a href="/about">About</a>`)

var page struct {
	Link string `grab:"a[href]" attr:"href" resolve:"url"`
}

err := grabber.GrabReader("https://example.com", html, &page)
```

### `GrabDocument`

Use `GrabDocument` when you already have a `goquery` document:

```go
doc, _ := goquery.NewDocumentFromReader(strings.NewReader(`<h1>Hello</h1>`))

var page struct {
	Title string `grab:"h1"`
}

err := grabber.GrabDocument("https://example.com", doc, &page)
```

## Grabber Configuration

`Grabber` controls transport, retry behavior, error strictness, and custom
converters.

```go
grabber := webgrab.New()
grabber.Timeout = 30
grabber.MaxRedirects = 5
grabber.StrictMode = false
grabber.RetryCount = 2
grabber.RetryDelay = 250 * time.Millisecond
grabber.RetryBackoff = 2.0
grabber.AllowedStatusCodes = []int{200, 202}
```

### Fields

#### `Timeout int`

Request timeout in seconds when WebGrab creates its own `http.Client`.

#### `MaxRedirects int`

Maximum redirect count when WebGrab creates its own `http.Client`.

#### `StrictMode bool`

Controls how slices of structs behave when individual items fail.

- `false`: invalid items are skipped
- `true`: invalid items are returned as errors

#### `UserAgent string`

User-Agent header to send on requests.

#### `HTTPClient *http.Client`

If set, WebGrab uses this client directly. When this is non-nil, `Timeout` and
`MaxRedirects` are not used to build another client.

#### `BeforeRequest func(*http.Request) error`

Runs before each request attempt. Useful for:

- headers
- auth
- cookies
- request validation

#### `AfterResponse func(*http.Response) error`

Runs after each response is received, before status handling. Useful for:

- custom validation
- logging
- rate-limit inspection

#### `AllowedStatusCodes []int`

Accepted response codes. If empty, WebGrab defaults to `200`.

#### `RetryCount int`

Number of retry attempts after the initial request.

#### `RetryDelay time.Duration`

Base delay between retries.

#### `RetryBackoff float64`

Backoff multiplier applied to the retry delay. Defaults to `1.0`.

## Struct Tags

### `grab:"selector"`

Primary CSS selector.

```go
Title string `grab:"h1"`
```

You can also use fallback selectors with `||`:

```go
Title string `grab:".headline||h1||title"`
```

Selectors are tried from left to right until one matches at least one element.

### `attr:"attribute"`

Reads an attribute instead of text.

```go
Link string `grab:"a[href]" attr:"href"`
```

### `extract:"regexp"`

Applies a regex and keeps the first capture group.

```go
ID int `grab:"a[href]" attr:"href" extract:"id=(\\d+)"`
```

### `filter:"regexp"`

Keeps only values that match the regex.

```go
HTMLLinks []string `grab:"a[href]" attr:"href" filter:".*\\.html$"`
```

### `context:"selector"`

Restricts scraping to a nested area.

For nested structs:

```go
type Profile struct {
	Name string `grab:".name"`
}

type Page struct {
	Profile Profile `context:".profile"`
}
```

For scalar slices, the selector runs once inside each context:

```go
type Page struct {
	Prices []int `grab:".price" context:".product"`
}
```

### `optional:"true"`

If nothing is found, leave the field at its zero value instead of returning an
error.

```go
Subtitle string `grab:".subtitle" optional:"true"`
```

### `default:"value"`

Fallback value for optional fields.

```go
Author string `grab:".author" optional:"true" default:"Unknown"`
```

`default` is only applied when `optional:"true"` is also set.

### `whitespace:"trim|collapse|preserve"`

Controls whitespace handling.

#### `trim`

Default behavior. Uses `strings.TrimSpace`.

#### `collapse`

Collapses all whitespace runs to a single space.

#### `preserve`

Leaves whitespace untouched.

Example:

```go
Summary string `grab:".summary" whitespace:"collapse"`
```

### `html:"true"`

Extracts inner HTML instead of text.

```go
Markup string `grab:".content" html:"true"`
```

`html:"true"` cannot be used with `attr:"..."`.

### `resolve:"url"`

Resolves relative values against the page or reader base URL.

```go
Image string `grab:"img" attr:"src" resolve:"url"`
```

This works with:

- `Grab`, using the final response URL
- `GrabReader`, using the supplied base URL
- `GrabDocument`, using the supplied base URL

### `layout:"..."`

Controls time parsing for `time.Time` and `[]time.Time`.

```go
Published time.Time `grab:"time" attr:"datetime" layout:"2006-01-02"`
```

If omitted, WebGrab uses `time.RFC3339`.

### `sep:"..."`

Joins multiple matched values into a single scalar field.

```go
TagLine string `grab:".tag" sep:" / "`
```

Without `sep`, scalar fields use the first matched value.

## Supported Field Types

WebGrab supports these built-in scalar types:

- `string`
- `bool`
- `int`, `int8`, `int16`, `int32`, `int64`
- `uint`, `uint8`, `uint16`, `uint32`, `uint64`
- `float32`, `float64`
- `time.Time`

It also supports:

- slices of those types
- nested structs
- slices of structs
- custom types through registered converters

### Built-in conversion example

```go
type Metrics struct {
	Enabled bool      `grab:".enabled"`
	Count   int       `grab:".count"`
	Total   uint      `grab:".total"`
	Score   float64   `grab:".score"`
	Date    time.Time `grab:".date" layout:"2006-01-02"`
}
```

## Custom Type Converters

Custom converters let you populate exact destination types that WebGrab does not
convert by default.

### Registering a converter

```go
type Slug struct {
	Value string
}

grabber.RegisterConverter(reflect.TypeOf(Slug{}), func(raw string) (any, error) {
	return Slug{Value: strings.ToUpper(raw)}, nil
})
```

### Using the custom type

```go
type Page struct {
	Slug  Slug   `grab:".slug"`
	Slugs []Slug `grab:".tags li"`
}
```

### Converter behavior

Converters:

- are stored per `Grabber`
- are matched by exact destination type
- run after extraction, whitespace normalization, default handling, and URL
  resolution
- may be used for both scalar fields and slices

If a converter returns the wrong type, WebGrab returns an error.

## Nested Structs and Contexts

Nested structs recurse into another schema.

```go
type Price struct {
	Amount string `grab:".amount"`
	Label  string `grab:".label"`
}

type Page struct {
	Price Price `context:".pricing"`
}
```

For slices of structs, `context` is required:

```go
type Item struct {
	Title string `grab:".title"`
	Link  string `grab:"a" attr:"href" resolve:"url"`
}

type Page struct {
	Items []Item `context:".item"`
}
```

## Slices and Aggregation

### Scalar slices

Scalar slices collect all successfully converted values.

```go
type Page struct {
	Links []string `grab:"a[href]" attr:"href"`
	IDs   []int    `grab:"a[href]" attr:"href" extract:"id=(\\d+)"`
}
```

### Struct slices

Struct slices scrape one item per `context` match.

By default:

- broken items are skipped
- valid items are retained

With `StrictMode = true`:

- broken items are returned as errors

### Scalar aggregation with `sep`

Use `sep` to join many matches into one scalar field:

```go
type Page struct {
	TagLine string `grab:".tag" sep:", "`
}
```

## URL Resolution

Relative links and media URLs are common in scraped HTML.

```go
type Page struct {
	Canonical string `grab:"link[rel=canonical]" attr:"href" resolve:"url"`
	Image     string `grab:"img.hero" attr:"src" resolve:"url"`
}
```

Examples:

- base URL: `https://example.com/posts/1`
- scraped `href`: `/about`
- resolved result: `https://example.com/about`

If `resolve:"url"` is used without a base URL, WebGrab returns an error.

## Whitespace and HTML Extraction

### Text extraction

By default, WebGrab extracts text.

```go
Body string `grab:".body"`
```

### HTML extraction

Use `html:"true"` to keep markup:

```go
BodyHTML string `grab:".body" html:"true" whitespace:"preserve"`
```

### Whitespace examples

Input:

```text
  hello
 world  
```

Results:

- `trim` -> `hello\n world`
- `collapse` -> `hello world`
- `preserve` -> original whitespace preserved

## Filtering and Regex Extraction

These features can be combined.

```go
type Page struct {
	Links []string `grab:"a[href]" attr:"href" filter:".*\\.html$"`
	ID    int      `grab:"a[href]" attr:"href" extract:"id=(\\d+)"`
}
```

Behavior:

- `filter` removes non-matching values
- `extract` transforms the kept value
- if `extract` fails, the field errors

## HTTP Behavior, Hooks, and Retries

### Custom client

```go
grabber.HTTPClient = &http.Client{
	Timeout: 15 * time.Second,
}
```

### Before request

```go
grabber.BeforeRequest = func(req *http.Request) error {
	req.Header.Set("Authorization", "Bearer token")
	return nil
}
```

### After response

```go
grabber.AfterResponse = func(resp *http.Response) error {
	if resp.Header.Get("X-Blocked") == "true" {
		return fmt.Errorf("blocked by upstream")
	}
	return nil
}
```

### Allowing non-200 responses

```go
grabber.AllowedStatusCodes = []int{200, 202}
```

### Retries

```go
grabber.RetryCount = 2
grabber.RetryDelay = 500 * time.Millisecond
grabber.RetryBackoff = 2.0
```

Retries apply to:

- network errors
- status codes `429`, `502`, `503`, `504`

Retries do not apply to:

- parse failures
- conversion failures
- selector or field errors

## Strict Mode

`StrictMode` affects slices of structs.

Given:

```go
type Item struct {
	Title string `grab:".title"`
}

type Page struct {
	Items []Item `context:".item"`
}
```

If one `.item` is missing `.title`:

- `StrictMode = false`: the broken item is skipped
- `StrictMode = true`: the scrape returns an error

This is useful because some sites are messy and partial results are often still
valuable.

## Errors

WebGrab now exposes typed errors.

### `FieldError`

Returned when a specific field fails due to:

- missing required selectors
- invalid tag combinations
- regex failures
- conversion failures
- nested field failures

Example:

```go
var fieldErr *webgrab.FieldError
if errors.As(err, &fieldErr) {
	fmt.Println(fieldErr.Field)
	fmt.Println(fieldErr.Selector)
	fmt.Println(fieldErr.Context)
}
```

### `StatusError`

Returned when an HTTP response status is not allowed.

```go
var statusErr *webgrab.StatusError
if errors.As(err, &statusErr) {
	fmt.Println(statusErr.StatusCode)
}
```

### `RequestError`

Returned for request-level failures:

- invalid URL
- request hook failure
- transport failure
- response hook failure

```go
var requestErr *webgrab.RequestError
if errors.As(err, &requestErr) {
	fmt.Println(requestErr.URL)
}
```

### Joined errors

WebGrab may return `errors.Join(...)` for multiple field failures. You can still
use `errors.As` to find a specific typed error within the joined result.

## Patterns and Recipes

### Use fallback selectors for fragile sites

```go
Title string `grab:".hero h1||article h1||title"`
```

### Keep optional fields non-fatal

```go
Subtitle string `grab:".subtitle" optional:"true"`
```

### Scrape only HTML pages

```go
Links []string `grab:"a[href]" attr:"href" filter:".*\\.html$"`
```

### Extract IDs from URLs

```go
IDs []int `grab:"a[href]" attr:"href" extract:"id=(\\d+)"`
```

### Resolve relative URLs

```go
Links []string `grab:"a[href]" attr:"href" resolve:"url"`
```

### Join repeated tags into one label

```go
Label string `grab:".tag" sep:", "`
```

### Parse timestamps with a custom layout

```go
Published time.Time `grab:"time" attr:"datetime" layout:"2006-01-02 15:04"`
```

### Keep inner HTML

```go
BodyHTML string `grab:".body" html:"true" whitespace:"preserve"`
```

## Defaults and Behavior Notes

- `New()` creates a `Grabber` with sensible defaults.
- `AllowedStatusCodes` defaults to `200`.
- `RetryBackoff` defaults to `1.0`.
- `time.Time` defaults to `time.RFC3339` if no `layout` is provided.
- Scalar fields use the first value unless `sep` is set.
- Slice fields keep all successfully converted values.
- Struct slices skip bad items unless `StrictMode` is enabled.
- `html:"true"` and `attr:"..."` cannot be combined.
- Custom converters are exact-type and per-`Grabber`.

## Final Advice

For stable scrapers:

- prefer fallback selectors
- mark non-essential fields as optional
- use `resolve:"url"` for links and images
- register custom converters for domain types
- use `StrictMode` only when you want partial rows to fail the scrape

For messy real-world sites, WebGrab works best when you treat optional content
as optional and reserve strict behavior for fields that are truly required.
