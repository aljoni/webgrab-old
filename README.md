# 🌍🤏 WebGrab

![GitHub](https://img.shields.io/github/license/aljoni/webgrab)
![GitHub release (with filter)](https://img.shields.io/github/v/release/aljoni/webgrab)
[![Go Report Card](https://goreportcard.com/badge/github.com/aljoni/webgrab)](https://goreportcard.com/report/github.com/aljoni/webgrab)
[![Go Reference](https://pkg.go.dev/badge/github.com/aljoni/webgrab.svg)](https://pkg.go.dev/github.com/aljoni/webgrab)


WebGrab is a simple Go library which allows for easy scraping of web pages. It is built on top of the [GoQuery](http://github.com/PuerkitoBio/goquery) library.

## Installation

```bash
go get github.com/aljoni/webgrab
```

## Usage

```go
package main

import (
    "fmt"

    "github.com/aljoni/webgrab"
)

type Page struct {
    Title    string `grab:"title"`
    Body     string `grab:"body"`
    Keywords string `grab:"meta[name=keywords]" attr:"content"`
}

func main() {
    page := Page{}
    
    grabber := webgrab.New()
    grabber.Timeout = 30
    grabber.MaxRedirects = 10
    if err := grabber.Grab("http://example.com", &page); err != nil {
        panic(err)
    }

    fmt.Println(page.Title)
    fmt.Println(page.Body)
    fmt.Println(page.Keywords)
}
```

### Tag Syntax

The defined tags are:

* `grab:"selector"` - The selector to use to grab the value.
* `attr:"attribute"` - The attribute of the selected element to grab.
* `extract:"regexp"` - A regular expression to extract a value from a string.
* `filter:"regexp"` - A regular expression to filter the value of a field.
* `context:"selector"` - Restricts the context for a nested struct or slice of structs.

The selector is a [GoQuery](http://godoc.org/github.com/PuerkitoBio/goquery) selector. The attribute is an
optional attribute of the selected element to grab. If no attribute is
specified, the text of the selected element will be grabbed.

---

### Slices

If the field is a slice, all matching elements will be grabbed. For example,
to grab all links from a page:

```go
type Page struct {
    Links []string `grab:"a[href]" attr:"href"`
}
```

---

### Nested Structs and Context Windows

You can use nested structs to grab values from a specific section of the page.  
With the new `context` tag, you can restrict the scraping context for a struct or a slice of structs.

#### Single Nested Struct with Context

```go
type Profile struct {
    Name  string `grab:".name"`
    Email string `grab:".email"`
}

type Page struct {
    Profile Profile `context:".profile-section"`
}
```
This will only search for `.name` and `.email` inside the first `.profile-section` element.

#### Slice of Structs with Context

```go
type Item struct {
    Title string `grab:".title"`
    Link  string `grab:"a" attr:"href"`
}

type Page struct {
    Items []Item `context:".item"`
}
```
This will find all elements matching `.item`, and for each, scrape the `.title` and the first `<a>`'s `href` inside that `.item`.

---

### Extract

The `extract` tag can be used to extract a value from a string using a regular
expression. For example, to extract the title from a Wikipedia page:

```go
type Page struct {
    Title string `grab:"title" extract:"(.+) - Wikipedia"`
}
```

---

### Filter

The `filter` tag can be used to filter the value of a field. For example, to
get all links that end with `.html`:

```go
type Page struct {
    Links []string `grab:"a[href]" attr:"href" filter:".*\\.html$"`
}
```
