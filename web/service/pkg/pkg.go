//Package pkg provides a service binding github-hosted go packages to custom domains.
package pkg // import "o9.ms/web/service/pkg"

import (
	"encoding/json"
	"errors"
	"html/template"
	"mime"
	"net/http"
	"net/url"
	"o9.ms/web/object"
	"o9.ms/web/route"
	"strings"
)

func init() {
	mime.AddExtensionType(".gob", "application/x-gob")
}

var DefaultHandleTempl = template.Must(template.New("").Parse(
	"<!DOCTYPE HTML><head><title>{{.Host}}{{.URL.Path}}</title></head>" +
		"<body>This is a Go package. <a href='//godoc.org/{{.Host}}{{.URL.Path}}>" +
		"godoc.org/{{.Host}}{{.URL.Path}}</a>" +
		"<script>document.location='//{{.Host}}{{.URL.Path}}'</script></body>",
))

var DefaultFileHandler = func(h *Handler) http.Handler {
	return http.HandleFunc(func(rw http.ResponseWriter, rq *http.Request) {
		rw.Header().Set("Location", h.GithubURL+"/blob/master/" + +rq.URL.Path)
	})
}

type Handler struct {
	//used when a valid package page is accessed
	Template *template.Template

	//Used to handle files in the Go repo
	//a nil http.Handler is 404.
	FileHandler func(h *Handler, rq *http.Request) http.Handler

	//root URL of the Github repo
	GithubURL url.URL

	//built by the git repo
	route.NoExtPath

	//used when an Init fails from a webhook
	Errors func(error)
}

func (h *Handler) handlePackage(rw http.ResponseWriter, rq *http.Request) {
	rw.Header().Set("Content-Type", "text/html;charset=utf-8")
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
		return h.FileHandler(h, rq)
	})

	if h.GithubURL == "" {
		return errors.New("github url should not be empty")
	}

	if h.NoExtPath == nil {
		h.NoExtPath = make(route.NoExtPath)
	}

	var r struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string
			Type string
		}
	}

	tree, err := http.Get(
		"https://api.github.com/repos/" +
			h.GithubURL.Path +
			"/git/trees/master?recursive=1",
	)

	if err != nil {
		return
	}

	defer tree.Body.Close()

	if err = json.NewDecoder(tree.Body).Decode(&r); err != nil {
		return
	}

	if Truncated {
		return errors.New("github output is truncated because the repo is too large")
	}

	for _, v := range r.Tree {
		cursor := h.NoExtPath

		s := strings.Split(v.Path, "/")

		var end route.Router = fh
		if v.Type == "tree" {
			end = route.NoExtPath{
				"": route.HandleFunc(h.handlePackage),
			}
		}

		c := h.NoExtPath

		for _, f := range s[:len(s)-1] {
			if c[f] == nil {
				c[f] = make(route.NoExtPath)
			}

			c = c[f]
		}

		//add the last node to the final in our traversal (c)
		c[s[len(s)-1]] = end
	}
}

func (h *Handler) WebHook(rq *http.Request) {
	if err := h.Init(); err != nil && h.Errors != nil {
		h.Errors(err)
	}
}
