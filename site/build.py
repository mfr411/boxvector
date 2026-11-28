#!/usr/bin/env python3
"""Build the documentation pages of boxvector.co from the repository markdown.

usage: build.py <repo-dir> <site-dir>
Writes <site-dir>/docs/**/index.html. The landing page (index.html) is left alone.
Needs: python3-markdown-it.
"""
import html, os, re, shutil, sys
from markdown_it import MarkdownIt

repo, site = sys.argv[1], sys.argv[2]
GH = "https://github.com/mfr411/boxvector"

# (url path, source file, nav title, language)
PAGES = [
    ("docs/",               "README.md",             "Getting started", "en"),
    ("docs/configuration/", "docs/configuration.md", "Configuration",  "en"),
    ("docs/cli/",           "docs/cli.md",           "CLI reference",  "en"),
    ("docs/vpp/",           "docs/vpp.md",           "VPP data plane", "en"),
    ("docs/id/",            "README.id.md",          "Mulai (Bahasa)", "id"),
]
URL_OF = {src: "/" + url for url, src, _, _ in PAGES}
URL_OF["README.md#cli"] = "/docs/cli/"

md = MarkdownIt("commonmark", {"html": True}).enable("table")

def slug(t):
    s = re.sub(r"<[^>]+>", "", t).lower()
    s = re.sub(r"[^\w\- ]", "", s).strip().replace(" ", "-")
    return s

def fix_link(href, src):
    if re.match(r"^(https?:|mailto:|#)", href):
        return href
    path, _, frag = href.partition("#")
    full = os.path.normpath(os.path.join(os.path.dirname(src), path)) if path else src
    if full in URL_OF:
        return URL_OF[full] + ("#" + frag if frag else "")
    if os.path.isdir(os.path.join(repo, full)):
        return f"{GH}/tree/main/{full}"
    return f"{GH}/blob/main/{full}" + ("#" + frag if frag else "")

def render(src):
    text = open(os.path.join(repo, src), encoding="utf-8").read()
    text = re.sub(r"<!-- cli:(begin|end) -->", "", text)
    text = re.sub(r"^<p align=\"center\">\s*<picture>.*?</picture>\s*</p>\s*", "", text, flags=re.S)
    text = text.replace('<p align="center">', "<p>")
    body = md.render(text)
    toc, seen = [], {}

    def head(m):
        lvl, inner = m.group(1), m.group(2)
        s = slug(inner) or "section"
        n = seen.get(s, 0); seen[s] = n + 1
        if n: s += f"-{n}"
        if lvl == "2":
            toc.append((s, re.sub(r"<[^>]+>", "", html.unescape(inner))))
        return f'<h{lvl} id="{s}">{inner}<a class="hl" href="#{s}" aria-label="link">#</a></h{lvl}>'
    body = re.sub(r"<h([1-4])>(.*?)</h\1>", head, body, flags=re.S)
    body = re.sub(r"<h1 id=\"configuration\">.*?</h1>", "", body, count=1, flags=re.S) if src.endswith("configuration.md") and body.count("<h1") > 1 else body
    body = re.sub(r'href="([^"]+)"', lambda m: 'href="%s"' % fix_link(html.unescape(m.group(1)), src), body)
    body = re.sub(r"<li>\s*(<p>)?\[ \]\s*", lambda m: '<li class="ck">' + (m.group(1) or "") + '<input type="checkbox" aria-label="done"> ', body)
    body = re.sub(r"<li>\s*(<p>)?\[[xX]\]\s*", lambda m: '<li class="ck">' + (m.group(1) or "") + '<input type="checkbox" checked aria-label="done"> ', body)
    body = re.sub(r"<table>", '<div class="tw"><table>', body)
    body = body.replace("</table>", "</table></div>")
    # copy button on every code block
    body = re.sub(r"<pre><code", '<pre data-copy><code', body)
    return body, toc

TPL = open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "docs.tpl.html"), encoding="utf-8").read()

def page(url, src, title, lang):
    body, toc = render(src)
    nav = "".join(
        ('<hr>' if l == "id" else '') + f'<a href="/{u}"{" class=on" if u == url else ""}>{html.escape(t)}</a>' for u, _, t, l in PAGES)
    side = "".join(f'<a href="#{s}">{html.escape(t)}</a>' for s, t in toc)
    return (TPL.replace("{{TITLE}}", html.escape(title)).replace("{{LANG}}", lang)
               .replace("{{NAV}}", nav).replace("{{TOC}}", side).replace("{{BODY}}", body)
               .replace("{{EDIT}}", f"{GH}/edit/main/{src}"))

out = os.path.join(site, "docs")
tmp = out + ".new"
shutil.rmtree(tmp, ignore_errors=True)
for url, src, title, lang in PAGES:
    d = os.path.join(site, url.rstrip("/") + ".new") if url != "docs/" else tmp
    if url != "docs/":
        d = os.path.join(tmp, url[len("docs/"):])
    os.makedirs(d, exist_ok=True)
    open(os.path.join(d, "index.html"), "w", encoding="utf-8").write(page(url, src, title, lang))
shutil.rmtree(out + ".old", ignore_errors=True)
if os.path.exists(out):
    os.rename(out, out + ".old")
os.rename(tmp, out)          # swap in one step: readers never see a half-built tree
shutil.rmtree(out + ".old", ignore_errors=True)
print("built", len(PAGES), "pages")
