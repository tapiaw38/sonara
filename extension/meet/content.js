// Reads Meet's live captions (they must be turned on: CC button) and reports
// who is speaking. Captions are used instead of the speaking-ring on the video
// tiles because their container has a stable aria role; the rest of Meet's
// markup is obfuscated class names that change often.
// ponytail: DOM scraping, breaks when Meet changes its captions markup.
// Upgrade path: an official Meet API with live speaker events, none exists today.

const CAPTIONS = /caption|subt[ií]tulo|legenda|untertitel|sous-titre/i;
const lastSent = new Map();

function captionsRegion(node) {
  const el = node.nodeType === Node.ELEMENT_NODE ? node : node.parentElement;
  const region = el && el.closest('[role="region"][aria-label]');
  return region && CAPTIONS.test(region.getAttribute("aria-label")) ? region : null;
}

// Each caption entry is avatar + name + text: climb from the changed node to
// the first ancestor that holds an avatar, its first text line is the name.
function speakerOf(node, region) {
  let el = node.nodeType === Node.ELEMENT_NODE ? node : node.parentElement;
  while (el && el !== region) {
    if (el.querySelector("img")) {
      const name = (el.innerText || "").split("\n")[0].trim();
      return name.length > 0 && name.length <= 80 ? name : null;
    }
    el = el.parentElement;
  }
  return null;
}

function report(name) {
  const now = Date.now();
  if (now - (lastSent.get(name) || 0) < 400) return;
  lastSent.set(name, now);
  chrome.runtime.sendMessage({ name, ts: now });
}

new MutationObserver((mutations) => {
  for (const mutation of mutations) {
    const region = captionsRegion(mutation.target);
    if (!region) continue;
    const name = speakerOf(mutation.target, region);
    if (name) report(name);
  }
}).observe(document.body, { childList: true, subtree: true, characterData: true });
