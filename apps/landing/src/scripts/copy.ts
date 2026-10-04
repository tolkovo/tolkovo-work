// "Copy" button on the install snippet. Hidden without JS; falls back to selecting the text.
for (const btn of document.querySelectorAll<HTMLButtonElement>("button[data-copy]")) {
  const { copy = "", label = "", copied = "", selected = "" } = btn.dataset;
  btn.hidden = false;
  let timer: ReturnType<typeof setTimeout> | undefined;

  const show = (text: string) => {
    btn.textContent = text;
    clearTimeout(timer);
    timer = setTimeout(() => (btn.textContent = label), 2500);
  };

  const selectSnippet = () => {
    const pre = btn.parentElement?.querySelector("pre");
    const sel = getSelection();
    if (!pre || !sel) return;
    const range = document.createRange();
    range.selectNodeContents(pre);
    sel.removeAllRanges();
    sel.addRange(range);
    show(selected);
  };

  btn.addEventListener("click", () => {
    if (!navigator.clipboard) {
      selectSnippet();
      return;
    }
    navigator.clipboard.writeText(copy).then(() => show(copied), selectSnippet);
  });
}
