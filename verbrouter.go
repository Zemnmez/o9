package fweight

import (
	"net/http"
)

func GetOnly(r Router) VerbRouter {
	return VerbRouter{
		"GET": r,
	}
}

var _ Router = make(VerbRouter)

var OptionsHandler func(i interface{}, rw http.ResponseWriter, rq *http.Request)
var MethodNotAllowed func(i interface{}, rw http.ResponseWriter, rq *http.Request)

func (v VerbRouter) Verbs() (ops []string) {
	ops = make([]string, len(v))
	if OptionsHandler != nil {
		ops = append(ops, "OPTIONS")
	}
	var i uint
	for k := range v {
		ops[i] = k
		i++
	}
	return
}

func (v VerbRouter) RouteHTTP(rq *http.Request) Router {
	if OptionsHandler != nil && rq.Method == "OPTIONS" {
		return Handle(http.HandlerFunc(func(rw http.ResponseWriter, rq *http.Request) {
			OptionsHandler(v.Verbs(), rw, rq)
		}))
	}
	if r := v.self()[rq.Method]; r != nil {
		return r
	} else {
		return Handle(http.HandlerFunc(func(rw http.ResponseWriter, rq *http.Request) {
			rw.WriteHeader(405)

			MethodNotAllowed(v.Verbs(), rw, rq)
		}))
	}
}

//A Router that routes based on verbs and provides
type VerbRouter map[string]Router

func (p VerbRouter) self() map[string]Router {
	if p == nil {
		p = make(VerbRouter)
	}
	return p
}

//func Verb adds an HTTP verb to this VerbRouter.
func (p VerbRouter) Verb(verb string, hf Router) VerbRouter {
	p = p.self()
	p[verb] = hf
	return p
}

func (p VerbRouter) Get(hf Router) VerbRouter {
	p.Verb("GET", hf)
	return p
}

func (p VerbRouter) Post(hf Router) VerbRouter {
	p.Verb("POST", hf)
	return p
}

func (p VerbRouter) Head(hf Router) VerbRouter {
	p.Verb("HEAD", hf)
	return p
}

func (p VerbRouter) Put(hf Router) VerbRouter {
	p.Verb("PUT", hf)
	return p
}

func (p VerbRouter) Delete(hf Router) VerbRouter {
	p.Verb("DELETE", hf)
	return p
}

func (p VerbRouter) Options(hf Router) VerbRouter {
	p.Verb("OPTIONS", hf)
	return p
}

func (p VerbRouter) Patch(hf Router) VerbRouter {
	p.Verb("PATCH", hf)
	return p
}
