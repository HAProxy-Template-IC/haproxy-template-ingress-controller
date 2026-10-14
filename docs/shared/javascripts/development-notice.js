;(function () {
  function showDevelopmentNotice() {
    var notice = document.querySelector("[data-development-docs]")
    if (notice) notice.hidden = !/^\/docs\/dev(?:\/|$)/.test(window.location.pathname)
  }
  showDevelopmentNotice()
  if (typeof document$ !== "undefined") document$.subscribe(showDevelopmentNotice)
})()
