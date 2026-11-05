// Marks the overview as out of date while its refresh fails, for example
// because the service stopped or the login ended with a restart of the service.
// Without it the page would keep showing the last state it got.
document.addEventListener("htmx:afterRequest", (e) => {
  if (e.detail.elt.id === "live") {
    document.body.classList.toggle("stale", !e.detail.successful);
  }
});
