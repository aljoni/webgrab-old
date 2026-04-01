# 🌍🤏 WebGrab

![GitHub](https://img.shields.io/github/license/aljoni/webgrab)
![GitHub release (with filter)](https://img.shields.io/github/v/release/aljoni/webgrab)
[![Go Report Card](https://goreportcard.com/badge/github.com/aljoni/webgrab)](https://goreportcard.com/report/github.com/aljoni/webgrab)
[![Go Reference](https://pkg.go.dev/badge/github.com/aljoni/webgrab.svg)](https://pkg.go.dev/github.com/aljoni/webgrab)

WebGrab is a small Go scraping library built on top of [goquery](http://github.com/PuerkitoBio/goquery). It keeps a tag-based API for simple scraping, while supporting typed conversion, fallback selectors, custom converters, request hooks, retries, and scraping from existing readers or documents.

## Installation

```bash
go get github.com/aljoni/webgrab
```

## Usage

```go
package main

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/aljoni/webgrab"
)

type Slug struct {
	Value string
}

type Post struct {
	Title     string    `grab:".hero h1||title"`
	Published time.Time `grab:"time" attr:"datetime"`
	Keywords  []string  `grab:"meta[name=keywords]" attr:"content" extract:"[^,]+"`
	Author    string    `grab:".author" optional:"true" default:"Unknown"`
	URL       string    `grab:"link[rel=canonical]" attr:"href" resolve:"url"`
	TagLine   string    `grab:".tag" sep:" / "`
	Slug      Slug      `grab:"meta[name=slug]" attr:"content"`
}

func main() {
	grabber := webgrab.New()
	grabber.Timeout = 30
	grabber.MaxRedirects = 10
	grabber.StrictMode = false
	grabber.RegisterConverter(reflect.TypeOf(Slug{}), func(raw string) (any, error) {
		return Slug{Value: strings.ToUpper(raw)}, nil
	})

	post := Post{}
	if err := grabber.Grab("https://example.com/post", &post); err != nil {
		panic(err)
	}

	fmt.Println(post.Title)
	fmt.Println(post.Published)
	fmt.Println(post.Keywords)
	fmt.Println(post.Author)
	fmt.Println(post.URL)
	fmt.Println(post.TagLine)
	fmt.Println(post.Slug.Value)
}
```

## Tag Syntax

Supported tags:

- `grab:"selector"`: CSS selector to scrape. Use `||` for ordered fallbacks, for example `grab:".headline||h1||title"`.
- `attr:"attribute"`: Extract an attribute instead of text.
- `extract:"regexp"`: Extract the first capture group from the scraped value.
- `filter:"regexp"`: Keep only values that match the regular expression.
- `context:"selector"`: Restrict scraping to a nested context. For scalar slices, the selector runs once inside each matching context.
- `optional:"true"`: Leave the field at its zero value when nothing is found.
- `default:"value"`: Fallback raw value when an optional field has no value.
- `whitespace:"trim|collapse|preserve"`: Control whitespace normalization. Default is `trim`.
- `html:"true"`: Extract inner HTML instead of text.
- `resolve:"url"`: Resolve the extracted value against the page or reader base URL.
- `layout:"..."`: Time layout for `time.Time` fields and slices. Defaults to `time.RFC3339`.
- `sep:"..."`: Join multiple matched values into a scalar field.

Notes:

- `html:"true"` and `attr:"..."` cannot be used together.
- `default` is only applied when `optional:"true"` is set.
- Slice fields collect all successfully converted values.
- Struct slice fields skip broken items by default. Set `Grabber.StrictMode = true` to return item errors instead.

## Supported Field Types

Built-in scalar and slice conversion works for:

- `string`
- `bool`
- `int`, `int8`, `int16`, `int32`, `int64`
- `uint`, `uint8`, `uint16`, `uint32`, `uint64`
- `float32`, `float64`
- `time.Time`

Nested structs and slices of structs are also supported.

For custom destination types, register an exact-type converter on a `Grabber`:

```go
type Slug struct {
	Value string
}

grabber.RegisterConverter(reflect.TypeOf(Slug{}), func(raw string) (any, error) {
	return Slug{Value: strings.ToUpper(raw)}, nil
})
```

## Grab From Existing HTML

You can scrape from a reader or an existing `goquery.Document` without making an HTTP request:

```go
html := strings.NewReader(`<article><h1>Hello</h1></article>`)

var page struct {
	Title string `grab:"h1"`
}

if err := grabber.GrabReader("https://example.com/post", html, &page); err != nil {
	panic(err)
}
```

## Request Control

`Grabber` supports custom request behavior:

- `HTTPClient`: Use your own `*http.Client`.
- `BeforeRequest`: Mutate or validate the request before every attempt.
- `AfterResponse`: Inspect or reject responses before status handling.
- `AllowedStatusCodes`: Accept non-200 responses.
- `RetryCount`, `RetryDelay`, `RetryBackoff`: Retry transient request failures and status codes `429`, `502`, `503`, and `504`.

## Errors

WebGrab exposes typed errors:

- `FieldError`: Selector, conversion, or field-configuration failures.
- `StatusError`: Disallowed HTTP status codes.
- `RequestError`: Request setup, hook, or transport failures.

These are returned directly or as part of `errors.Join`, so `errors.As` works for structured handling.
