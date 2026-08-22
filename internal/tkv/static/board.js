(() => {
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
