// Boot order (APP.md §2.4): the store subscribes to every event at module
// scope when it is imported, before the first await; then the whole state
// is fetched. App mounts after both.
import { mount } from "svelte";
import "./app.css";
import { store } from "./lib/state.svelte";
import App from "./App.svelte";

// The window's backdrop (APP.md §6, Controls: the ground): the shell opens
// the page at /?backdrop=mica only when it made the window over Mica, so
// the page is told what the shell applied rather than guessing; app.css's
// Mica block reads the attribute alone, and without it nothing changes. Set
// before the mount, in the same task, so the first frame the app draws is
// the material's.
if (new URLSearchParams(location.search).get("backdrop") === "mica") document.documentElement.dataset.backdrop = "mica";

store.boot();
mount(App, { target: document.getElementById("app")! });
