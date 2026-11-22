// Content scripts can't reach localhost (CORS / Private Network Access), the
// service worker can thanks to host_permissions.
chrome.runtime.onMessage.addListener((event) => {
  fetch("http://127.0.0.1:47823/meet/speaking", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(event),
  }).catch(() => {}); // Sonara closed: nothing to do
});
