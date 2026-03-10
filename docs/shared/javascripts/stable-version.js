// Override MkDocs Material's source version widget to show only stable releases.
// Material uses GitLab's releases/permalink/latest which doesn't filter pre-releases.
;(function () {
  var found = false
  var observer = new MutationObserver(function () {
    var el = document.querySelector(".md-source__fact--version")
    if (!el || !el.textContent.trim() || found) return
    found = true
    observer.disconnect()
    if (/-(alpha|beta|rc)[.\d]/.test(el.textContent)) {
      var repo = el.closest("[data-md-source]")
      if (!repo) return
      var href = repo.getAttribute("href") || ""
      var match = href.match(/gitlab\.com\/(.+?)(?:\/?\s*$)/)
      if (!match) return
      var apiUrl =
        "https://gitlab.com/api/v4/projects/" +
        encodeURIComponent(match[1]) +
        "/releases?per_page=20"
      fetch(apiUrl)
        .then(function (r) {
          return r.json()
        })
        .then(function (releases) {
          for (var i = 0; i < releases.length; i++) {
            if (/^v?\d+\.\d+\.\d+$/.test(releases[i].tag_name)) {
              el.textContent = releases[i].tag_name
              return
            }
          }
        })
        .catch(function () {})
    }
  })
  observer.observe(document.body, {
    childList: true,
    subtree: true,
    characterData: true,
  })
})()
