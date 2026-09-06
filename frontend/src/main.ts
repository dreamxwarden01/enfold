// Boot order (APP.md §2.4): the store subscribes to every event at module
// scope when it is imported, before the first await; then the whole state
// is fetched. App mounts after both.
import { mount } from "svelte";
import "./app.css";
import { store } from "./lib/state.svelte";
import App from "./App.svelte";

store.boot();
mount(App, { target: document.getElementById("app")! });
