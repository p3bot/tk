(() => {
  const layersKey = "tkv.board-layers";

  const loadLayers = () => {
    try {
      const raw = localStorage.getItem(layersKey);
      if (!raw) {
        return {};
      }
      const parsed = JSON.parse(raw);
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
        return {};
      }
      return parsed;
    } catch {
      return {};
    }
  };

  const saveLayers = (scope, layers) => {
    if (!scope) {
      return;
    }
    try {
      const all = loadLayers();
      if (!layers.backlog && !layers.archived) {
        delete all[scope];
      } else {
        all[scope] = {
          backlog: !!layers.backlog,
          archived: !!layers.archived,
        };
      }
      localStorage.setItem(layersKey, JSON.stringify(all));
    } catch {
      // private mode / quota: page query still works for this visit
    }
  };

  const layersFromSearch = (search) => {
    const q = new URLSearchParams(search);
    return {
      backlog: q.get("backlog") === "1",
      archived: q.get("archived") === "1",
    };
  };

  const hasLayerParams = (search) => {
    const q = new URLSearchParams(search);
    return q.has("backlog") || q.has("archived");
  };

  const boardScopeFromHref = (href) => {
    let u;
    try {
      u = new URL(href, location.origin);
    } catch {
      return "";
    }
    const m = u.pathname.match(/^\/scope\/([^/]+)$/);
    return m ? m[1] : "";
  };

  const hrefWithLayers = (href, layers) => {
    const u = new URL(href, location.origin);
    if (layers.backlog) {
      u.searchParams.set("backlog", "1");
    } else {
      u.searchParams.delete("backlog");
    }
    if (layers.archived) {
      u.searchParams.set("archived", "1");
    } else {
      u.searchParams.delete("archived");
    }
    return u.pathname + u.search + u.hash;
  };

  const scopeEl = document.querySelector("[data-board-scope]");
  const scope = scopeEl ? scopeEl.getAttribute("data-board-scope") : "";
  if (scope && hasLayerParams(location.search)) {
    saveLayers(scope, layersFromSearch(location.search));
  }

  const stored = loadLayers();
  if (scope && !hasLayerParams(location.search)) {
    const layers = stored[scope];
    if (layers && (layers.backlog || layers.archived)) {
      const next = hrefWithLayers(location.href, layers);
      const cur = location.pathname + location.search + location.hash;
      if (next !== cur) {
        location.replace(next);
        return;
      }
    }
  }

  for (const a of document.querySelectorAll('a[href^="/scope/"]')) {
    if (a.matches("[data-board-switch]")) {
      continue;
    }
    const href = a.getAttribute("href");
    const name = boardScopeFromHref(href);
    if (!name) {
      continue;
    }
    const layers = stored[name];
    if (!layers || (!layers.backlog && !layers.archived)) {
      continue;
    }
    a.setAttribute("href", hrefWithLayers(href, layers));
  }

  for (const sw of document.querySelectorAll("[data-board-switch]")) {
    sw.addEventListener("click", () => {
      const href = sw.getAttribute("href");
      if (!href) {
        return;
      }
      let u;
      try {
        u = new URL(href, location.origin);
      } catch {
        return;
      }
      saveLayers(scope || boardScopeFromHref(href), layersFromSearch(u.search));
    });
    sw.addEventListener("keydown", (e) => {
      if (e.key === " " || e.key === "Spacebar") {
        e.preventDefault();
        sw.click();
      }
    });
  }

  const input = document.querySelector("[data-board-filter]");
  const board = document.querySelector(".kanban");
  if (!input || !board) {
    return;
  }
  const cols = board.querySelectorAll(".col");
  input.addEventListener("input", () => {
    const q = input.value.trim().toLowerCase();
    for (const col of cols) {
      let n = 0;
      for (const card of col.querySelectorAll(".card")) {
        const hay = (card.getAttribute("data-filter") || "").toLowerCase();
        const show = q === "" || hay.includes(q);
        card.hidden = !show;
        const order = card.querySelector(".order");
        if (order) {
          // Find is visual-only; do not order against hidden neighbours.
          order.hidden = q !== "";
        }
        if (show) {
          n++;
        }
      }
      const count = col.querySelector(".count");
      if (count) {
        count.textContent = String(n);
      }
      col.hidden = q !== "" && n === 0;
    }
  });
})();
