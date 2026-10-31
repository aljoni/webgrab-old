# 🌍🤏 WebGrab

![GitHub](https://img.shields.io/github/license/aljoni/webgrab)
![GitHub release (with filter)](https://img.shields.io/github/v/release/aljoni/webgrab)
[![Go Report Card](https://goreportcard.com/badge/github.com/aljoni/webgrab)](https://goreportcard.com/report/github.com/aljoni/webgrab)
[![Go Reference](https://pkg.go.dev/badge/github.com/aljoni/webgrab.svg)](https://pkg.go.dev/github.com/aljoni/webgrab)

WebGrab is a small Go scraping library built on top of [goquery](http://github.com/PuerkitoBio/goquery).

It provides a simple struct-tag API for:

- scraping from URLs, readers, or existing `goquery` documents
- extracting text, HTML, or attributes
- converting values into strings, numbers, booleans, times, slices, nested structs, or custom types
- handling optional fields, fallback selectors, URL resolution, hooks, retries, and strict/permissive list parsing

For the full guide, advanced features, and detailed examples, see [docs/WIKI.md](docs/WIKI.md).

## Installation

```bash
go get github.com/aljoni/webgrab
```

## Quick Start

```go
package main

import (
	"fmt"

	"github.com/aljoni/webgrab"
)

type Page struct {
	Title    string   `grab:"h1||title"`
	Keywords string `grab:"meta[name=keywords]" attr:"content"`
	Author   string   `grab:".author" optional:"true" default:"Unknown"`
}

func main() {
	grabber := webgrab.New()

	var page Page
	if err := grabber.Grab("https://example.com", &page); err != nil {
		panic(err)
	}

	fmt.Println(page.Title)
	fmt.Println(page.Keywords)
	fmt.Println(page.Author)
}
```

## Common Tags

- `grab:"selector"`: CSS selector to scrape. Use `||` for fallbacks.
- `attr:"href"`: read an attribute instead of text.
- `extract:"regexp"`: keep the first capture group.
- `filter:"regexp"`: keep only matching values.
- `context:"selector"`: scope nested structs or repeated items.
- `optional:"true"`: leave missing values at zero value.
- `default:"value"`: fallback for optional fields.
- `resolve:"url"`: resolve relative links against the page URL.
- `layout:"..."`: time parsing layout for `time.Time`.
- `sep:"..."`: join multiple matches into one scalar field.

More tags and behavior notes are covered in [docs/WIKI.md](docs/WIKI.md).

## Supported Types

Built-in support includes:

- `string`
- `bool`
- `int`, `uint`, `float64`, and the other standard integer/float variants
- `time.Time`
- slices of the supported scalar types
- nested structs and slices of structs

You can also register custom converters per `Grabber`. See the [custom type converters](docs/WIKI.md#custom-type-converters) section in the wiki.

## Entry Points

- `Grab(url, &dst)`: fetch a page and scrape it
- `GrabReader(baseURL, reader, &dst)`: scrape existing HTML
- `GrabDocument(baseURL, doc, &dst)`: scrape an existing `goquery.Document`

## Transport Features

`Grabber` also supports:

- custom `HTTPClient`
- `BeforeRequest` and `AfterResponse` hooks
- allowed non-200 status codes
- retry configuration
- optional `robots.txt` enforcement for `Grab`
- `StrictMode` for slices of structs

See the wiki sections on [HTTP behavior, hooks, and retries](docs/WIKI.md#http-behavior-hooks-and-retries) and [strict mode](docs/WIKI.md#strict-mode) for details.

## Errors

WebGrab exposes typed errors:

- `FieldError`
- `StatusError`
- `RequestError`

See the [errors](docs/WIKI.md#errors) section in the wiki for examples with `errors.As`.
