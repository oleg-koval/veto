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

const routeDemo = document.querySelector("[data-route-demo]");
const routeToggle = document.querySelector("[data-route-toggle]");
const routeReplay = document.querySelector("[data-route-replay]");
const routeStatus = document.querySelector("[data-route-status]");

if (routeDemo && routeToggle && routeReplay) {
  document.body.classList.add("js-ready");
  const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
  const stageDelay = 1700;
  let timer = 0;
  let stage = 1;
  let playing = false;
  let userPaused = false;
  let wasPlaying = false;
  let inView = true;

  const setStage = (nextStage) => {
    stage = nextStage;
    routeDemo.dataset.routeStage = String(stage);
  };

  const setStatus = (message) => {
    if (routeStatus) routeStatus.textContent = message;
  };

  const syncControl = () => {
    routeDemo.dataset.routePaused = String(!playing);
    routeToggle.setAttribute("aria-pressed", String(playing));
    routeToggle.textContent = playing ? "Pause sequence" : "Play sequence";
  };

  const stopTimer = () => {
    if (timer) window.clearTimeout(timer);
    timer = 0;
  };

  const finish = () => {
    stopTimer();
    playing = false;
    syncControl();
    setStatus(reduceMotion.matches ? "Reduced motion · route complete" : "Sequence complete · replay anytime");
  };

  const advance = () => {
    if (!playing) return;
    if (stage >= 5) {
      finish();
      return;
    }
    setStage(stage + 1);
    timer = window.setTimeout(advance, stageDelay);
  };

  const start = () => {
    stopTimer();
    if (reduceMotion.matches) {
      setStage(5);
      playing = false;
      syncControl();
      setStatus("Reduced motion · route complete");
      return;
    }
    if (document.hidden || !inView) {
      playing = false;
      wasPlaying = true;
      syncControl();
      setStatus(document.hidden ? "Paused while tab is hidden" : "Paused while offscreen");
      return;
    }
    playing = true;
    userPaused = false;
    syncControl();
    setStatus("Sequence playing");
    timer = window.setTimeout(advance, stageDelay);
  };

  const pauseForVisibility = (message) => {
    if (!playing) return;
    wasPlaying = true;
    playing = false;
    stopTimer();
    syncControl();
    setStatus(message);
  };

  routeToggle.addEventListener("click", () => {
    if (playing) {
      userPaused = true;
      playing = false;
      stopTimer();
      syncControl();
      setStatus("Paused · press Play sequence to continue");
      return;
    }
    if (stage >= 5) setStage(1);
    start();
  });

  routeReplay.addEventListener("click", () => {
    setStage(1);
    start();
  });

  document.addEventListener("visibilitychange", () => {
    if (document.hidden) {
      pauseForVisibility("Paused while tab is hidden");
    } else if (wasPlaying && !userPaused && stage < 5) {
      wasPlaying = false;
      start();
    }
  });

  if ("IntersectionObserver" in window) {
    const routeObserver = new IntersectionObserver(([entry]) => {
      inView = entry.isIntersecting;
      if (!entry.isIntersecting) {
        pauseForVisibility("Paused while offscreen");
      } else if (wasPlaying && !userPaused && stage < 5) {
        wasPlaying = false;
        start();
      }
    }, { threshold: 0.2 });
    routeObserver.observe(routeDemo);
  }

  const handleMotionPreference = () => {
    stopTimer();
    if (reduceMotion.matches) {
      playing = false;
      setStage(5);
      syncControl();
      setStatus("Reduced motion · route complete");
    } else if (stage >= 5) {
      setStage(1);
      start();
    }
  };

  if (typeof reduceMotion.addEventListener === "function") {
    reduceMotion.addEventListener("change", handleMotionPreference);
  } else if (typeof reduceMotion.addListener === "function") {
    reduceMotion.addListener(handleMotionPreference);
  }

  setStage(1);
  start();
}
