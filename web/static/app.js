(() => {
  const scrollKey = "momo-page-scroll";
  const url = new URL(window.location.href);
  if (url.searchParams.has("recorded")) {
    url.searchParams.delete("recorded");
    history.replaceState(history.state, "", url.pathname + url.search + url.hash);
  }

  const savedScroll = sessionStorage.getItem(scrollKey);
  if (savedScroll !== null) {
    history.scrollRestoration = "manual";
    window.scrollTo(0, Number(savedScroll));
    sessionStorage.removeItem(scrollKey);
    window.addEventListener("load", () => { history.scrollRestoration = "auto"; }, { once: true });
  }

  document.querySelectorAll('.trip-actions form[action="/trips"]').forEach((form) => {
    form.addEventListener("submit", () => sessionStorage.setItem(scrollKey, String(window.scrollY)));
  });
})();

(() => {
  const login = document.querySelector("[data-passkey-login]");
  const register = document.querySelector("[data-passkey-register]");
  if (!login && !register) return;

  const root = login || register;
  const button = root.querySelector("[data-passkey-submit]");
  const error = root.querySelector("[data-passkey-error]");
  const decode = (value) => {
    const padding = "=".repeat((4 - value.length % 4) % 4);
    const raw = atob((value + padding).replace(/-/g, "+").replace(/_/g, "/"));
    return Uint8Array.from(raw, (character) => character.charCodeAt(0));
  };
  const encode = (value) => {
    let raw = "";
    for (const byte of new Uint8Array(value)) raw += String.fromCharCode(byte);
    return btoa(raw).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  };
  const creationOptions = (options) => {
    const publicKey = options.publicKey;
    publicKey.challenge = decode(publicKey.challenge);
    publicKey.user.id = decode(publicKey.user.id);
    publicKey.excludeCredentials = (publicKey.excludeCredentials || []).map((item) => ({ ...item, id: decode(item.id) }));
    return publicKey;
  };
  const requestOptions = (options) => {
    const publicKey = options.publicKey;
    publicKey.challenge = decode(publicKey.challenge);
    publicKey.allowCredentials = (publicKey.allowCredentials || []).map((item) => ({ ...item, id: decode(item.id) }));
    return publicKey;
  };
  const credentialJSON = (credential) => {
    const response = {
      clientDataJSON: encode(credential.response.clientDataJSON),
    };
    if (credential.response.attestationObject) {
      response.attestationObject = encode(credential.response.attestationObject);
      response.transports = credential.response.getTransports?.() || [];
    } else {
      response.authenticatorData = encode(credential.response.authenticatorData);
      response.signature = encode(credential.response.signature);
      response.userHandle = credential.response.userHandle ? encode(credential.response.userHandle) : null;
    }
    return {
      id: credential.id,
      rawId: encode(credential.rawId),
      type: credential.type,
      authenticatorAttachment: credential.authenticatorAttachment,
      clientExtensionResults: credential.getClientExtensionResults(),
      response,
    };
  };
  const post = async (url, body) => {
    const response = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error?.message || "Passkey request failed.");
    return result;
  };
  const run = async () => {
    error.hidden = true;
    button.disabled = true;
    try {
      if (!window.isSecureContext || !navigator.credentials || !window.PublicKeyCredential) {
        throw new Error("Passkeys require HTTPS, or localhost during development.");
      }
      if (register) {
        const username = register.elements.username.value.trim();
        const options = await post("/auth/register/begin", { username });
        const credential = await navigator.credentials.create({ publicKey: creationOptions(options) });
        const result = await post("/auth/register/finish", credentialJSON(credential));
        window.location.assign(result.redirect);
      } else {
        const options = await post("/auth/login/begin");
        const credential = await navigator.credentials.get({ publicKey: requestOptions(options) });
        const result = await post("/auth/login/finish", credentialJSON(credential));
        window.location.assign(result.redirect);
      }
    } catch (cause) {
      error.textContent = cause.name === "NotAllowedError" ? "Passkey request canceled or timed out." : cause.message;
      error.hidden = false;
      button.disabled = false;
    }
  };
  if (register) register.addEventListener("submit", (event) => { event.preventDefault(); run(); });
  else button.addEventListener("click", run);
})();

(() => {
  document.addEventListener("click", async (event) => {
    const action = event.target.closest("[data-delete-trip]");
    if (!action) return;

    const dialog = action.closest("[data-tui-dialog-content]");
    const error = dialog.querySelector("[data-delete-trip-error]");
    action.disabled = true;
    error.hidden = true;
    try {
      const response = await fetch(`/api/v1/trips/${action.dataset.deleteTrip}`, { method: "DELETE" });
      if (!response.ok) throw new Error("delete failed");
      sessionStorage.setItem("momo-page-scroll", String(window.scrollY));
      window.location.assign("/");
    } catch {
      error.hidden = false;
      action.disabled = false;
    }
  });
})();

(() => {
  const panel = document.querySelector("[data-push-panel]");
  if (!panel) return;

  const status = panel.querySelector("[data-push-status]");
  const toggle = panel.querySelector("[data-push-toggle]");
  const isIOS = /iPhone|iPad|iPod/.test(navigator.userAgent) ||
    (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
  panel.hidden = false;
  if (!isIOS) {
    status.textContent = "Notifications are currently available when Momo is installed on an iPhone.";
    toggle.hidden = true;
    return;
  }
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
