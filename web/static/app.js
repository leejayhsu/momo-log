(() => {
  const panel = document.querySelector("[data-push-panel]");
  if (!panel) return;

  const status = panel.querySelector("[data-push-status]");
  const toggle = panel.querySelector("[data-push-toggle]");
  const isIOS = /iPhone|iPad|iPod/.test(navigator.userAgent) ||
    (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
  if (!isIOS) return;

  panel.hidden = false;
  if (!("serviceWorker" in navigator) || !("PushManager" in window) || !("Notification" in window)) {
    status.textContent = "Notifications require iOS 16.4 or later.";
    toggle.hidden = true;
    return;
  }
  if (!window.navigator.standalone) {
    status.textContent = "Add Momo to your Home Screen, then open it there to enable notifications.";
    toggle.hidden = true;
    return;
  }

  let registration;
  let subscription;

  const showSubscription = () => {
    const enabled = Boolean(subscription);
    status.textContent = enabled
      ? "This iPhone will be alerted when a trip is logged."
      : "Get an alert whenever someone logs a trip.";
    toggle.textContent = enabled ? "Disable notifications" : "Enable notifications";
    toggle.dataset.enabled = String(enabled);
    toggle.disabled = false;
  };

  const decodeKey = (value) => {
    const padding = "=".repeat((4 - value.length % 4) % 4);
    const raw = atob((value + padding).replace(/-/g, "+").replace(/_/g, "/"));
    return Uint8Array.from(raw, (character) => character.charCodeAt(0));
  };

  const initialize = async () => {
    try {
      registration = await navigator.serviceWorker.register("/sw.js");
      subscription = await registration.pushManager.getSubscription();
      showSubscription();
    } catch {
      status.textContent = "Notifications could not be initialized. Reload and try again.";
      toggle.hidden = true;
    }
  };

  toggle.addEventListener("click", async () => {
    toggle.disabled = true;
    try {
      if (subscription) {
        const endpoint = subscription.endpoint;
        const response = await fetch("/api/v1/push-subscriptions", {
          method: "DELETE",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ endpoint }),
        });
        if (!response.ok) throw new Error("unsubscribe failed");
        await subscription.unsubscribe();
        subscription = null;
      } else {
        const permission = await Notification.requestPermission();
        if (permission !== "granted") {
          status.textContent = permission === "denied"
            ? "Notifications are blocked in iPhone Settings."
            : "Notification permission was not granted.";
          toggle.disabled = false;
          return;
        }
        const configResponse = await fetch("/api/v1/push-config");
        if (!configResponse.ok) throw new Error("config failed");
        const config = await configResponse.json();
        subscription = await registration.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: decodeKey(config.public_key),
        });
        const response = await fetch("/api/v1/push-subscriptions", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(subscription),
        });
        if (!response.ok) {
          await subscription.unsubscribe();
          subscription = null;
          throw new Error("subscribe failed");
        }
      }
      showSubscription();
    } catch {
      status.textContent = "Could not update notifications. Please try again.";
      toggle.disabled = false;
    }
  });

  initialize();
})();
