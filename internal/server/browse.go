package server

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var errBadBrowse = errors.New("bad browse url")

const browseCookie = "exe_browse"

// handleBrowse reverse-proxies an external page so the in-desk Browser
// iframe (same origin) can show a full site: HTML, CSS, JS, images.
//
// Two URL shapes:
//
//	/v1/browse?url=https://example.com/x
//	/v1/browse/https/example.com/x?q=1
//
// The path form is what rewritten CSS/JS/img use, so relative url() in
// stylesheets resolve on this host instead of becoming /v1/browse?url=foo.
// Auth: Bearer, ?token=, or the exe_browse cookie set on the first hit.
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	target, err := browseTarget(r)
	if err != nil || target == nil {
		http.Error(w, "bad url", http.StatusBadRequest)
		return
	}
	if tok := browseToken(r); tok != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     browseCookie,
			Value:    tok,
			Path:     "/v1/browse",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   8 * 3600,
		})
	}

	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if method != http.MethodGet && method != http.MethodHead {
		body = io.LimitReader(r.Body, 8<<20)
	}
	req, err := http.NewRequestWithContext(r.Context(), method, target.String(), body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
	if a := r.Header.Get("Accept"); a != "" {
		req.Header.Set("Accept", a)
	} else {
		req.Header.Set("Accept", "*/*")
	}
	if l := r.Header.Get("Accept-Language"); l != "" {
		req.Header.Set("Accept-Language", l)
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("Accept-Encoding", "identity")

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 8 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "fetch: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		http.Error(w, "read: "+err.Error(), http.StatusBadGateway)
		return
	}
	final := resp.Request.URL
	lct := strings.ToLower(ct)
	switch {
	case strings.Contains(lct, "text/html"):
		raw = rewriteHTML(raw, final)
		ct = "text/html; charset=utf-8"
	case strings.Contains(lct, "text/css"):
		raw = []byte(rewriteCSS(string(raw), final))
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Cache-Control", "private, max-age=60")
	h.Del("X-Frame-Options")
	// Allow scripts/styles the page already referenced. frame-ancestors so
	// the desk iframe can hold it; unsafe-inline because we inject a helper.
	h.Set("Content-Security-Policy", "frame-ancestors *; default-src * data: blob: 'unsafe-inline' 'unsafe-eval'; frame-src 'self' data: blob:; child-src 'self' data: blob:; script-src * data: blob: 'unsafe-inline' 'unsafe-eval'; style-src * data: blob: 'unsafe-inline'")
	if loc := resp.Header.Get("Location"); loc != "" {
		if u, err := final.Parse(loc); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			h.Set("Location", browsePath(u))
		}
	}
	w.WriteHeader(resp.StatusCode)
	if method != http.MethodHead {
		_, _ = w.Write(raw)
	}
}

func browseToken(r *http.Request) string {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" {
		got = r.URL.Query().Get("token")
	}
	if got == "" {
		if c, err := r.Cookie(browseCookie); err == nil {
			got = c.Value
		}
	}
	return got
}

func browseTarget(r *http.Request) (*url.URL, error) {
	if raw := strings.TrimSpace(r.URL.Query().Get("url")); raw != "" {
		if !strings.Contains(raw, "://") {
			raw = "https://" + raw
		}
		u, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, errBadBrowse
		}
		return u, nil
	}
	rest := r.PathValue("path")
	if rest == "" {
		rest = strings.TrimPrefix(r.URL.Path, "/v1/browse/")
		if rest == r.URL.Path || rest == "" {
			return nil, errBadBrowse
		}
	}
	// https/example.com/foo/bar
	scheme, rest, ok := strings.Cut(rest, "/")
	if !ok || (scheme != "http" && scheme != "https") {
		return nil, errBadBrowse
	}
	host, path, _ := strings.Cut(rest, "/")
	if host == "" {
		return nil, errBadBrowse
	}
	u := &url.URL{Scheme: scheme, Host: host, Path: "/" + path}
	q := r.URL.Query()
	q.Del("token")
	u.RawQuery = q.Encode()
	return u, nil
}

func browsePath(u *url.URL) string {
	p := "/v1/browse/" + u.Scheme + "/" + u.Host + u.EscapedPath()
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		p += "#" + u.Fragment
	}
	return p
}

func rewriteHTML(raw []byte, base *url.URL) []byte {
	doc, err := html.Parse(strings.NewReader(string(raw)))
	if err != nil {
		return raw
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			keep := n.Attr[:0]
			for _, a := range n.Attr {
				key := strings.ToLower(a.Key)
				switch key {
				case "integrity", "crossorigin", "nonce":
					continue // SRI / CORS break after we proxy
				case "href", "src", "action", "poster", "data-src", "data-href", "formaction":
					if u := resolveAttr(base, a.Val); u != "" {
						a.Val = browsePath(mustURL(u))
					}
				case "srcset":
					a.Val = rewriteSrcset(base, a.Val)
				}
				keep = append(keep, a)
			}
			n.Attr = keep
			if n.DataAtom == atom.Base {
				n.Attr = nil
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	injectBrowseHelper(doc, base)
	var b strings.Builder
	if err := html.Render(&b, doc); err != nil {
		return raw
	}
	return []byte(b.String())
}

func mustURL(abs string) *url.URL {
	u, err := url.Parse(abs)
	if err != nil {
		return &url.URL{}
	}
	return u
}

func injectBrowseHelper(doc *html.Node, base *url.URL) {
	js := `(function(){
  var remoteBase=` + strconv.Quote(base.String()) + `;
  var urlAttrs={href:1,src:1,action:1,poster:1,"data-src":1,"data-href":1,formaction:1};
  function skipURL(u){
    if(typeof u!=="string") return true;
    var v=u.trim();
    return !v || v.charAt(0)==="#" || /^(javascript|data|mailto|tel|blob):/i.test(v);
  }
  function alreadyProxied(u){
    try{
      var local=new URL(u, location.href);
      return local.origin===location.origin && local.pathname.indexOf("/v1/browse/")===0;
    }catch(e){return false;}
  }
  function wrap(u){
    try{
      if(skipURL(u)) return u;
      if(alreadyProxied(u)) return u;
      var abs=new URL(u, remoteBase);
      if(abs.protocol!=="http:"&&abs.protocol!=="https:") return u;
      return "/v1/browse/"+abs.protocol.replace(":","")+"/"+abs.host+abs.pathname+(abs.search||"")+(abs.hash||"");
    }catch(e){return u;}
  }
  var os=Element.prototype.setAttribute;
  Element.prototype.setAttribute=function(k,v){
    if(urlAttrs[String(k).toLowerCase()]) v=wrap(String(v));
    return os.call(this,k,v);
  };
  function patchURLProp(ctorName, prop){
    try{
      var ctor=window[ctorName];
      if(!ctor||!ctor.prototype) return;
      var d=Object.getOwnPropertyDescriptor(ctor.prototype, prop);
      if(!d||!d.set||!d.get) return;
      Object.defineProperty(ctor.prototype, prop, {
        configurable: true,
        enumerable: d.enumerable,
        get: function(){ return d.get.call(this); },
        set: function(v){ return d.set.call(this, wrap(String(v))); }
      });
    }catch(e){}
  }
  [
    ["HTMLAnchorElement","href"],["HTMLLinkElement","href"],["HTMLAreaElement","href"],
    ["HTMLIFrameElement","src"],["HTMLImageElement","src"],["HTMLScriptElement","src"],
    ["HTMLSourceElement","src"],["HTMLVideoElement","poster"],["HTMLFormElement","action"]
  ].forEach(function(x){ patchURLProp(x[0],x[1]); });
  function rewriteEl(n){
    if(!n||n.nodeType!==1||!n.getAttribute) return;
    Object.keys(urlAttrs).forEach(function(a){
      var cur=n.getAttribute(a);
      if(cur==null) return;
      var next=wrap(cur);
      if(next!==cur) os.call(n,a,next);
    });
    for(var c=n.firstElementChild;c;c=c.nextElementSibling) rewriteEl(c);
  }
  new MutationObserver(function(ms){
    ms.forEach(function(m){
      if(m.type==="attributes") rewriteEl(m.target);
      m.addedNodes&&m.addedNodes.forEach(rewriteEl);
    });
  }).observe(document.documentElement,{subtree:true,childList:true,attributes:true,attributeFilter:Object.keys(urlAttrs)});
  var of=window.fetch;
  window.fetch=function(input,init){
    if(typeof input==="string") input=wrap(input);
    else if(input&&input.url) input=new Request(wrap(input.url),input);
    return of.call(this,input,init);
  };
  var oo=XMLHttpRequest.prototype.open;
  XMLHttpRequest.prototype.open=function(m,u){
    arguments[1]=wrap(u);
    return oo.apply(this,arguments);
  };
})();`
	script := &html.Node{Type: html.ElementNode, Data: "script", DataAtom: atom.Script}
	script.AppendChild(&html.Node{Type: html.TextNode, Data: js})
	// prefer <head>, else document
	var head *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Head {
			head = n
			return
		}
		for c := n.FirstChild; c != nil && head == nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	if head != nil {
		head.InsertBefore(script, head.FirstChild)
	} else {
		doc.AppendChild(script)
	}
}

func resolveAttr(base *url.URL, v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.HasPrefix(v, "#") || strings.HasPrefix(v, "javascript:") ||
		strings.HasPrefix(v, "data:") || strings.HasPrefix(v, "mailto:") ||
		strings.HasPrefix(v, "blob:") || strings.HasPrefix(v, "/v1/browse/") {
		return ""
	}
	u, err := base.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.String()
}

func rewriteSrcset(base *url.URL, v string) string {
	parts := strings.Split(v, ",")
	for i, p := range parts {
		f := strings.Fields(strings.TrimSpace(p))
		if len(f) == 0 {
			continue
		}
		if u := resolveAttr(base, f[0]); u != "" {
			f[0] = browsePath(mustURL(u))
			parts[i] = strings.Join(f, " ")
		}
	}
	return strings.Join(parts, ", ")
}

func rewriteCSS(css string, base *url.URL) string {
	var b strings.Builder
	rest := css
	for {
		i := strings.Index(strings.ToLower(rest), "url(")
		if i < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:i+4])
		rest = rest[i+4:]
		j := strings.Index(rest, ")")
		if j < 0 {
			b.WriteString(rest)
			break
		}
		inner := strings.TrimSpace(rest[:j])
		inner = strings.Trim(inner, `"'`)
		if u := resolveAttr(base, inner); u != "" {
			b.WriteString(`"` + browsePath(mustURL(u)) + `"`)
		} else {
			b.WriteString(rest[:j])
		}
		rest = rest[j:]
	}
	return b.String()
}
