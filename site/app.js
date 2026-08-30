const copyButton = document.querySelector("[data-copy]");
const copyStatus = document.querySelector("[data-copy-status]");
const installCommand = document.querySelector("[data-install-command]");

const selectCommand = () => {
  if (!installCommand) return;
  const selection = window.getSelection();
  const range = document.createRange();
  range.selectNodeContents(installCommand);
  selection.removeAllRanges();
  selection.addRange(range);
};

if (copyButton) {
  copyButton.addEventListener("click", async () => {
    const command = copyButton.dataset.copy;
    if (copyButton.dataset.fallback === "true") {
      selectCommand();
      if (copyStatus) copyStatus.textContent = "Command selected. Press Command-C or Control-C to copy it.";
      return;
    }
    try {
      await navigator.clipboard.writeText(command);
      copyButton.textContent = "Copied";
      copyButton.dataset.fallback = "false";
      if (copyStatus) copyStatus.textContent = "Installation command copied.";
      window.setTimeout(() => { copyButton.textContent = "Copy"; }, 1600);
    } catch {
      copyButton.textContent = "Select command";
      copyButton.dataset.fallback = "true";
      selectCommand();
      if (copyStatus) copyStatus.textContent = "Clipboard unavailable. The command is selected; press Command-C or Control-C to copy it.";
    }
  });
}

const year = document.querySelector("[data-year]");
if (year) year.textContent = new Date().getFullYear();
