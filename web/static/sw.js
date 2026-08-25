self.addEventListener("push", (event) => {
  let notification = {
    title: "Momo went outside",
    body: "A bathroom trip was logged.",
    url: "/",
  };
  if (event.data) {
    try {
      notification = { ...notification, ...event.data.json() };
    } catch {
      notification.body = event.data.text();
    }
  }
  event.waitUntil(self.registration.showNotification(notification.title, {
    body: notification.body,
    icon: "/static/icon.svg",
    badge: "/static/icon.svg",
    data: { url: notification.url },
  }));
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const target = new URL(event.notification.data?.url || "/", self.location.origin).href;
  event.waitUntil(clients.matchAll({ type: "window", includeUncontrolled: true }).then((windows) => {
    for (const windowClient of windows) {
      if (windowClient.url.startsWith(self.location.origin) && "focus" in windowClient) {
        windowClient.navigate(target);
        return windowClient.focus();
      }
    }
    return clients.openWindow(target);
  }));
});
