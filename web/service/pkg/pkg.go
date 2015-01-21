//Package pkg provides a service binding github-hosted go packages to custom domains.
package pkg // import "o9.ms/web/service/pkg"

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"mime"
	"net/http"
	"net/url"
	"o9.ms/web/route"
	"strings"
)

func init() {
	mime.AddExtensionType(".gob", "application/x-gob")
}

var DefaultHandleTempl = template.Must(template.New("").Parse(
	"<!DOCTYPE HTML><head><title>{{.Host}}{{.URL.Path}}</title>" +
		`<meta name="go-import" content="{{.Host}}{{.URL.Path}} git https://github.com/{{.Host}}{{.URL.Path}}"></head>` +
		"<body>This is a Go package. <a href='//godoc.org/{{.Host}}{{.URL.Path}}'>" +
		"godoc.org/{{.Host}}{{.URL.Path}}</a>" +
		"<script>document.location='//godoc.org/{{.Host}}{{.URL.Path}}'</script></body>",
))

var DefaultFileHandler = func(h *Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, rq *http.Request) {
		rw.Header().Set(
			"Location",
			h.GithubURL.String()+"/blob/master/"+rq.URL.Path,
		)
	})
}

type Handler struct {
	//used when a valid package page is accessed
	Template *template.Template

	//Used to handle files in the Go repo
	//a nil http.Handler is 404.
	FileHandler func(h *Handler, rq *http.Request) http.Handler

	//root URL of the Github repo
	GithubURL *url.URL

	//built by the git repo
	route.NoExtPath

	//used when an Init fails from a webhook
	Errors func(error)
}

func (h *Handler) handlePackage(rw http.ResponseWriter, rq *http.Request) {
	rw.Header().Set("Content-Type", "text/html;charset=utf-8")
	rw.Header().Set("Refresh", "0; url=//godoc.org/")

	if h.Template == nil {
		h.Template = DefaultHandleTempl
	}

	if err := h.Template.Execute(rw, rq); err != nil {
		h.Errors(err)
		rw.Write([]byte(err.Error()))
		rw.WriteHeader(500)
		return
	}
}

func (h *Handler) Init() (err error) {
	fh := route.RouterFunc(func(rq *http.Request) route.Router {
		if h := h.FileHandler(h, rq); h != nil {
			return route.Handle(h)
		}

		return nil
	})

	if h.GithubURL == nil {
		return errors.New("github url should not be empty")
	}

	h.NoExtPath = make(route.NoExtPath)

	h.NoExtPath[""] = route.HandleFunc(h.handlePackage)

	var r struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string
			Type string
		}
	}

	tree, err := http.Get(
		"https://api.github.com/repos" +
			h.GithubURL.Path +
			"/git/trees/master?recursive=1",
	)

	if err != nil {
		return
	}

	defer tree.Body.Close()

	if tree.StatusCode != 200 {
		return fmt.Errorf("%s -- %s", tree.Status, tree.Request.URL.String())
	}

	if err = json.NewDecoder(tree.Body).Decode(&r); err != nil {
		return
	}

	if r.Truncated {
		return errors.New("github output is truncated because the repo is too large")
	}

	for _, v := range r.Tree {

		s := strings.Split(v.Path, "/")

		var end route.Router = fh
		if v.Type == "tree" {
			end = route.NoExtPath{
				"": route.HandleFunc(h.handlePackage),
			}
		}

		var c route.Router = h.NoExtPath

		for _, f := range s[:len(s)-1] {
			cp := c.(route.NoExtPath)
			if cp[f] == nil {
				cp[f] = make(route.NoExtPath)
			}

			c = cp[f]
		}

		//add the last node to the final in our traversal (c)
		c.(route.NoExtPath)[s[len(s)-1]] = end
	}
	return
}

func (h *Handler) WebHook(rq *http.Request) {
	if err := h.Init(); err != nil && h.Errors != nil {
		h.Errors(err)
	}
}
