<script>
  import { onMount, tick } from "svelte";
  import {
    HasConfig,
    LoadConfig,
    SaveConfig,
    TestConnection,
    ClearConfig,
    ListBuckets,
    ListObjects,
    DeleteObjects,
    DeletePrefix,
    DownloadObject,
    DownloadObjects,
    EstimatePrefixes,
    GetDownloadConcurrency,
    SetDownloadConcurrency,
    GetBucketStats,
    GetConfig,
    PreviewObject,
    SetInlinePreviews,
    SetViewMode,
    SetDeleteEnabled,
    SearchObjects,
    SetPreviewSizeLimit,
  } from "../wailsjs/go/main/App.js";
  import { EventsOn } from "../wailsjs/runtime/runtime.js";

  const DEFAULT_PREVIEW_SIZE = 524288; // 512 KB — default image preview cap

  // --- State ---
  let view = "loading"; // loading | login | browser | settings
  let error = "";
  let loading = false;

  // --- Settings ---
  let settingsAccountID = "";
  let settingsAccessKeyID = "";
  let settingsSecretAccessKey = "";
  let settingsApiToken = "";
  let settingsConcurrency = 4;
  let settingsError = "";
  let settingsSaving = false;
  let settingsInlinePreviews = false;
  let settingsDeleteEnabled = false;
  let settingsPreviewSizeLimit = DEFAULT_PREVIEW_SIZE;
  let confirmDisconnect = false;

  async function openSettings() {
    settingsError = "";
    confirmDisconnect = false;
    try {
      const cfg = await GetConfig();
      if (cfg) {
        settingsAccountID = cfg.account_id ?? "";
        settingsAccessKeyID = cfg.access_key_id ?? "";
        settingsSecretAccessKey = cfg.secret_access_key ?? "";
        settingsApiToken = cfg.api_token ?? "";
        settingsConcurrency = cfg.download_concurrency || 4;
        settingsInlinePreviews = cfg.inline_previews ?? false;
        settingsDeleteEnabled = cfg.delete_enabled ?? false;
        settingsPreviewSizeLimit = cfg.preview_size_limit || DEFAULT_PREVIEW_SIZE;
        viewMode = cfg.view_mode || "list";
      }
    } catch (_) {}
    view = "settings";
  }

  async function saveSettings() {
    settingsError = "";
    settingsSaving = true;
    try {
      await TestConnection(settingsAccountID, settingsAccessKeyID, settingsSecretAccessKey);
      await SaveConfig(settingsAccountID, settingsAccessKeyID, settingsSecretAccessKey, settingsApiToken);
      await SetDownloadConcurrency(settingsConcurrency);
      dlConcurrency = settingsConcurrency;
      await toggleInlinePreviews(settingsInlinePreviews);
      await setDeleteEnabled(settingsDeleteEnabled);
      await SetPreviewSizeLimit(settingsPreviewSizeLimit);
      previewSizeLimit = settingsPreviewSizeLimit;
      // Reload bucket list and stats with new credentials
      bucketStats = {};
      view = "browser";
      await Promise.all([loadBuckets(), loadConcurrency(), loadDisplayPrefs()]);
    } catch (e) {
      settingsError = String(e);
    } finally {
      settingsSaving = false;
    }
  }

  function cancelSettings() {
    settingsError = "";
    view = "browser";
  }

  // --- Log panel ---
  let logs = [];
  let logOpen = false;
  let logEl;

  onMount(() => {
    const off = EventsOn("log", (msg) => {
      const ts = new Date().toLocaleTimeString();
      logs = [...logs, `[${ts}] ${msg}`];
      if (!logOpen) logOpen = true;
      tick().then(() => {
        if (logEl) logEl.scrollTop = logEl.scrollHeight;
      });
    });

    const onKeyDown = (e) => {
      if (e.key === "Shift") shiftHeld = true;
      if (e.altKey && e.key === "ArrowLeft")  { e.preventDefault(); goBack(); }
      if (e.altKey && e.key === "ArrowRight") { e.preventDefault(); goForward(); }
      // XF86Back / XF86Forward — emitted by some Linux mice as key events
      if (e.key === "BrowserBack")    { e.preventDefault(); goBack(); }
      if (e.key === "BrowserForward") { e.preventDefault(); goForward(); }
    };
    const onKeyUp = (e) => { if (e.key === "Shift") shiftHeld = false; };
    window.addEventListener("keydown", onKeyDown);
    window.addEventListener("keyup",   onKeyUp);

    return () => {
      off();
      window.removeEventListener("keydown", onKeyDown);
      window.removeEventListener("keyup",   onKeyUp);
    };
  });

  function clearLogs() {
    logs = [];
  }

  // Login form
  let accountID = "";
  let accessKeyID = "";
  let secretAccessKey = "";
  let apiToken = "";

  // Download concurrency
  let dlConcurrency = 4;
  let dlMaxConcurrency = 100;

  async function loadConcurrency() {
    try {
      const [cur, max] = await GetDownloadConcurrency();
      dlConcurrency = cur;
      dlMaxConcurrency = max;
    } catch (_) {}
  }

  async function loadDisplayPrefs() {
    try {
      const cfg = await GetConfig();
      initInlinePreviews(cfg);
    } catch (_) {}
  }

  async function applyDlConcurrency(n) {
    dlConcurrency = n;
    try {
      await SetDownloadConcurrency(n);
    } catch (e) {
      error = String(e);
    }
  }

  // Bucket stats: keyed by bucket name
  // value: undefined (not started), "loading", stats object, or { error }
  let bucketStats = {};

  async function loadBucketStats(name) {
    if (bucketStats[name] === "loading") return;
    bucketStats[name] = "loading";
    bucketStats = bucketStats;
    try {
      const stats = await GetBucketStats(name);
      bucketStats[name] = stats;
    } catch (e) {
      bucketStats[name] = { error: String(e) };
    }
    bucketStats = bucketStats;
  }

  // Load stats for all buckets concurrently, respecting dlConcurrency.
  async function loadAllBucketStats(names) {
    let active = 0;
    let i = 0;
    await new Promise((resolve) => {
      function next() {
        while (i < names.length && active < dlConcurrency) {
          const name = names[i++];
          active++;
          loadBucketStats(name).finally(() => {
            active--;
            if (i < names.length) {
              next();
            } else if (active === 0) {
              resolve();
            }
          });
        }
        if (i >= names.length && active === 0) resolve();
      }
      next();
    });
  }

  // Browser state
  let buckets = [];
  let currentBucket = "";
  let currentPrefix = "";
  // Navigation history — each entry is { bucket, prefix }
  // bucket = "" means the bucket list view.
  let navHistory = [];
  let navForward = [];
  $: canGoBack    = navHistory.length > 0;
  $: canGoForward = navForward.length > 0;
  let objects = [];
  let folders = [];
  let selected = new Set();
  let selectedFolders = new Set();
  let continuationToken = "";
  let hasMore = false;

  // Init: check if config exists
  (async () => {
    try {
      const has = await HasConfig();
      if (has) {
        await LoadConfig();
        view = "browser";
        await Promise.all([loadBuckets(), loadConcurrency(), loadDisplayPrefs()]);
      } else {
        view = "login";
      }
    } catch (e) {
      view = "login";
    }
  })();

  // --- Login ---
  async function handleLogin() {
    error = "";
    loading = true;
    try {
      await TestConnection(accountID, accessKeyID, secretAccessKey);
      await SaveConfig(accountID, accessKeyID, secretAccessKey, apiToken);
      view = "browser";
      await Promise.all([loadBuckets(), loadConcurrency(), loadDisplayPrefs()]);
    } catch (e) {
      error = String(e);
    } finally {
      loading = false;
    }
  }

  async function handleLogout() {
    try {
      await ClearConfig();
    } catch (_) {}
    currentBucket = "";
    currentPrefix = "";
    navHistory = [];
    navForward = [];
    objects = [];
    folders = [];
    buckets = [];
    selected = new Set();
    selectedFolders = new Set();
    view = "login";
    accountID = "";
    accessKeyID = "";
    secretAccessKey = "";
    apiToken = "";
  }

  // --- Browser ---
  async function loadBuckets() {
    try {
      buckets = await ListBuckets();
      // Fire-and-forget: load stats for all buckets in background.
      loadAllBucketStats(buckets.map((b) => b.name));
    } catch (e) {
      error = String(e);
    }
  }

  // Push current location onto back-stack and clear forward.
  function pushNav() {
    navHistory = [...navHistory, { bucket: currentBucket, prefix: currentPrefix }];
    navForward = [];
  }

  // Apply a location without touching history (used by goBack/goForward).
  async function applyLocation({ bucket, prefix }) {
    if (!bucket) {
      currentBucket = "";
      currentPrefix = "";
      objects = [];
      folders = [];
      selected = new Set();
      selectedFolders = new Set();
      clearExpanded();
      clearSearch();
      lastSelectedIndex = null;
    } else {
      currentBucket = bucket;
      currentPrefix = prefix;
      selected = new Set();
      selectedFolders = new Set();
      clearExpanded();
      clearSearch();
      lastSelectedIndex = null;
      await loadObjects();
    }
  }

  async function goBack() {
    if (!canGoBack) return;
    const dest = navHistory[navHistory.length - 1];
    navHistory = navHistory.slice(0, -1);
    navForward = [{ bucket: currentBucket, prefix: currentPrefix }, ...navForward];
    await applyLocation(dest);
  }

  async function goForward() {
    if (!canGoForward) return;
    const dest = navForward[0];
    navForward = navForward.slice(1);
    navHistory = [...navHistory, { bucket: currentBucket, prefix: currentPrefix }];
    await applyLocation(dest);
  }

  async function selectBucket(name) {
    pushNav();
    currentBucket = name;
    currentPrefix = "";
    await loadObjects();
  }

  async function loadObjects(token = "") {
    if (!token) { thumbs = {}; clearExpanded(); clearSearch(); lastSelectedIndex = null; }
    loading = true;
    error = "";
    selected = new Set();
    selectedFolders = new Set();
    try {
      const result = await ListObjects(currentBucket, currentPrefix, token);
      if (token) {
        objects = [...objects, ...result.objects];
        folders = [...new Set([...folders, ...result.prefixes])];
      } else {
        objects = result.objects;
        folders = result.prefixes;
      }
      hasMore = result.is_truncated;
      continuationToken = result.next_token;
    } catch (e) {
      error = String(e);
    } finally {
      loading = false;
    }
  }

  function navigateInto(prefix) {
    pushNav();
    currentPrefix = prefix;
    loadObjects();
  }

  function navigateUp() {
    if (currentPrefix !== "") {
      pushNav();
      const parts = currentPrefix.split("/").filter(Boolean);
      parts.pop();
      currentPrefix = parts.length > 0 ? parts.join("/") + "/" : "";
      loadObjects();
    }
  }

  function backToBuckets() {
    pushNav();
    currentBucket = "";
    currentPrefix = "";
    objects = [];
    folders = [];
    selected = new Set();
    selectedFolders = new Set();
    clearExpanded();
  }

  // --- Selection ---
  let shiftHeld = false;
  let lastSelectedIndex = null; // anchor for range selection

  // handleSelect is used as on:change on list-view checkboxes.
  // The browser has already toggled input.checked before this fires,
  // so we just sync our sets and handle range expansion.
  function handleSelect(idx, item) {
    if (shiftHeld && lastSelectedIndex !== null) {
      // Range: add every selectable item between anchor and here.
      const lo = Math.min(lastSelectedIndex, idx);
      const hi = Math.max(lastSelectedIndex, idx);
      for (let i = lo; i <= hi; i++) {
        const it = flatItems[i];
        if (it.kind === "folder" && it.depth === 0) selectedFolders.add(it.prefix);
        else if (it.kind === "object" && it.depth === 0) selected.add(it.obj.key);
      }
      selected = selected;
      selectedFolders = selectedFolders;
      // Anchor stays fixed on range extend.
    } else {
      // Normal toggle — mirror whatever the browser just did to the checkbox.
      if (item.kind === "folder") {
        if (selectedFolders.has(item.prefix)) selectedFolders.delete(item.prefix);
        else selectedFolders.add(item.prefix);
        selectedFolders = selectedFolders;
      } else {
        if (selected.has(item.obj.key)) selected.delete(item.obj.key);
        else selected.add(item.obj.key);
        selected = selected;
      }
      lastSelectedIndex = idx;
    }
  }

  function toggleSelect(key) {
    if (selected.has(key)) selected.delete(key);
    else selected.add(key);
    selected = selected;
  }

  function toggleSelectFolder(prefix) {
    if (selectedFolders.has(prefix)) selectedFolders.delete(prefix);
    else selectedFolders.add(prefix);
    selectedFolders = selectedFolders;
  }

  function selectAll() {
    if (totalSelected > 0) {
      selected = new Set();
      selectedFolders = new Set();
    } else {
      selected = new Set(objects.map((o) => o.key));
      selectedFolders = new Set(folders);
    }
    lastSelectedIndex = null;
  }

  $: totalSelected = selected.size + selectedFolders.size;

  // --- Folder expand (inline tree) ---
  // expandedFolders: Set of prefix strings currently expanded
  // folderContents: prefix -> { loading, error, objects, folders }
  let expandedFolders = new Set();
  let folderContents = {};

  async function toggleExpand(prefix) {
    if (expandedFolders.has(prefix)) {
      expandedFolders.delete(prefix);
      expandedFolders = expandedFolders;
      return;
    }
    expandedFolders.add(prefix);
    expandedFolders = expandedFolders;
    if (!folderContents[prefix]) {
      await loadFolderContents(prefix);
    }
  }

  async function loadFolderContents(prefix) {
    folderContents[prefix] = { loading: true };
    folderContents = folderContents;
    try {
      const result = await ListObjects(currentBucket, prefix, "");
      folderContents[prefix] = {
        loading: false,
        objects: result.objects,
        folders: result.prefixes,
      };
    } catch (e) {
      folderContents[prefix] = { loading: false, error: String(e) };
    }
    folderContents = folderContents;
  }

  // Build a flat list of renderable items from the current folders/objects,
  // recursively inserting expanded children inline.
  function buildFlatList(folderList, objectList, expanded, contents, depth = 0) {
    const items = [];
    for (const prefix of folderList) {
      items.push({ kind: "folder", prefix, depth });
      if (expanded.has(prefix)) {
        const c = contents[prefix];
        if (!c || c.loading) {
          items.push({ kind: "loading", depth: depth + 1 });
        } else if (c.error) {
          items.push({ kind: "error", message: c.error, depth: depth + 1 });
        } else {
          items.push(...buildFlatList(c.folders ?? [], c.objects ?? [], expanded, contents, depth + 1));
        }
      }
    }
    for (const obj of objectList) {
      items.push({ kind: "object", obj, depth });
    }
    return items;
  }

  $: flatItems = buildFlatList(folders, objects, expandedFolders, folderContents);

  // Clear expand state on navigation.
  function clearExpanded() {
    expandedFolders = new Set();
    folderContents = {};
  }

  // --- Search ---
  let searchQuery = "";
  let searchResults = null; // null = not searching, [] = results (may be empty)
  let searching = false;

  async function runSearch() {
    const q = searchQuery.trim();
    if (!q) return;
    searching = true;
    searchResults = null;
    error = "";
    try {
      searchResults = await SearchObjects(currentBucket, currentPrefix, q);
    } catch (e) {
      error = String(e);
      searchResults = null;
    }
    searching = false;
  }

  function clearSearch() {
    searchQuery = "";
    searchResults = null;
    searching = false;
  }

  function handleSearchKey(e) {
    if (e.key === "Enter") runSearch();
    if (e.key === "Escape") clearSearch();
  }

  // --- Delete ---
  let confirmDelete = false;
  let deleting = false;

  async function handleDelete() {
    if (!confirmDelete) {
      confirmDelete = true;
      return;
    }
    deleting = true;
    error = "";
    try {
      // Delete individual objects
      if (selected.size > 0) {
        await DeleteObjects(currentBucket, Array.from(selected));
      }
      // Delete folder prefixes (all objects under them)
      for (const prefix of selectedFolders) {
        await DeletePrefix(currentBucket, prefix);
      }
      confirmDelete = false;
      await loadObjects();
    } catch (e) {
      error = String(e);
    } finally {
      deleting = false;
    }
  }

  function cancelDelete() {
    confirmDelete = false;
  }

  // --- Download ---
  async function handleDownload(key) {
    error = "";
    try {
      await DownloadObject(currentBucket, key);
    } catch (e) {
      error = String(e);
    }
  }

  async function handleDownloadSelected() {
    if (selected.size === 0 && selectedFolders.size === 0) return;

    // If only flat files selected, go straight to download.
    if (selectedFolders.size === 0) {
      error = "";
      try {
        await DownloadObjects(currentBucket, Array.from(selected));
      } catch (e) {
        error = String(e);
      }
      return;
    }

    // Folders selected — scan first.
    await scanFolderDownload();
  }

  // Folder download state
  const WARN_SIZE    = 5 * 1024 * 1024 * 1024; // 5 GB
  const WARN_OBJECTS = 10_000;

  let folderScan = null;   // null | "scanning" | { keys, object_count, total_size, warned }
  let folderScanError = "";

  async function scanFolderDownload() {
    folderScan = "scanning";
    folderScanError = "";
    error = "";
    try {
      const est = await EstimatePrefixes(currentBucket, Array.from(selectedFolders));
      const allKeys = [...Array.from(selected), ...est.keys];
      const needsWarn = est.total_size > WARN_SIZE || est.object_count > WARN_OBJECTS;
      if (needsWarn) {
        folderScan = { keys: allKeys, object_count: est.object_count, total_size: est.total_size, warned: true };
      } else {
        folderScan = null;
        await runFolderDownload(allKeys);
      }
    } catch (e) {
      folderScan = null;
      folderScanError = String(e);
    }
  }

  async function runFolderDownload(keys) {
    folderScan = null;
    folderScanError = "";
    error = "";
    try {
      await DownloadObjects(currentBucket, keys);
    } catch (e) {
      error = String(e);
    }
  }

  function cancelFolderDownload() {
    folderScan = null;
    folderScanError = "";
  }

  // --- Preview ---
  let preview = null; // null | "loading" | { type, mime_type, content, data_url, size, truncated, key }

  async function openPreview(key) {
    preview = "loading";
    try {
      const p = await PreviewObject(currentBucket, key);
      preview = { ...p, key };
    } catch (e) {
      preview = { type: "error", key, message: String(e) };
    }
  }

  function closePreview() {
    preview = null;
  }

  function parseCSV(text) {
    return text.trim().split("\n").map((line) => {
      // Naive CSV split — handles quoted fields with commas.
      const row = [];
      let cur = "", inQuote = false;
      for (let i = 0; i < line.length; i++) {
        const ch = line[i];
        if (ch === '"') { inQuote = !inQuote; }
        else if (ch === "," && !inQuote) { row.push(cur); cur = ""; }
        else { cur += ch; }
      }
      row.push(cur);
      return row;
    });
  }

  function prettyJSON(text) {
    try { return JSON.stringify(JSON.parse(text), null, 2); }
    catch (_) { return text; }
  }

  // --- Inline thumbnails ---
  const IMAGE_EXTS = new Set([
    "jpg","jpeg","png","gif","webp","avif","bmp","ico","svg","tiff","tif"
  ]);

  function isImage(key) {
    const ext = key.split(".").pop()?.toLowerCase() ?? "";
    return IMAGE_EXTS.has(ext);
  }

  let viewMode = "list"; // "list" | "grid"
  let deleteEnabled = false;
  let previewSizeLimit = DEFAULT_PREVIEW_SIZE;

  async function setViewMode(mode) {
    viewMode = mode;
    try { await SetViewMode(mode); } catch (_) {}
  }

  async function setDeleteEnabled(val) {
    deleteEnabled = val;
    try { await SetDeleteEnabled(val); } catch (e) { error = String(e); }
  }

  let inlinePreviews = false;
  // thumbs: key -> "loading" | dataURL | "error"
  let thumbs = {};

  async function loadThumb(key) {
    thumbs[key] = "loading";
    thumbs = thumbs;
    try {
      const p = await PreviewObject(currentBucket, key);
      thumbs[key] = p.type === "image" ? p.data_url : "error";
    } catch (_) {
      thumbs[key] = "error";
    }
    thumbs = thumbs;
  }

  // Load thumbnails for all image keys in the current listing, capped at dlConcurrency.
  async function loadVisibleThumbs(keys) {
    const pending = keys.filter(k => !thumbs[k]);
    if (!pending.length) return;
    let active = 0, i = 0;
    await new Promise((resolve) => {
      function next() {
        while (i < pending.length && active < dlConcurrency) {
          const key = pending[i++];
          active++;
          loadThumb(key).finally(() => {
            active--;
            if (i < pending.length) next();
            else if (active === 0) resolve();
          });
        }
        if (i >= pending.length && active === 0) resolve();
      }
      next();
    });
  }

  // Trigger thumb loads: always in grid mode, or when inline previews enabled in list mode.
  $: if ((inlinePreviews || viewMode === "grid") && objects.length) {
    loadVisibleThumbs(objects.filter(o => isImage(o.key)).map(o => o.key));
  }

  function initInlinePreviews(cfg) {
    inlinePreviews = cfg?.inline_previews ?? false;
    viewMode = cfg?.view_mode || "list";
    deleteEnabled = cfg?.delete_enabled ?? false;
    previewSizeLimit = cfg?.preview_size_limit || DEFAULT_PREVIEW_SIZE;
  }

  async function toggleInlinePreviews(val) {
    inlinePreviews = val;
    if (!val) thumbs = {};
    try {
      await SetInlinePreviews(val);
    } catch (e) {
      error = String(e);
    }
  }

  // --- Helpers ---
  function displayName(key) {
    // Strip current prefix to show just the filename
    const name = key.startsWith(currentPrefix)
      ? key.slice(currentPrefix.length)
      : key;
    return name;
  }

  function folderName(prefix) {
    const trimmed = prefix.endsWith("/") ? prefix.slice(0, -1) : prefix;
    const parts = trimmed.split("/");
    return parts[parts.length - 1] + "/";
  }

  function formatSize(bytes) {
    if (bytes === 0) return "0 B";
    const units = ["B", "KB", "MB", "GB", "TB"];
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return (bytes / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0) + " " + units[i];
  }

  function formatDate(iso) {
    if (!iso) return "";
    return new Date(iso).toLocaleDateString(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
  }
</script>

{#if view === "loading"}
  <div class="center-screen">
    <p class="muted">Loading...</p>
  </div>
{:else if view === "login"}
  <div class="center-screen">
    <div class="login-card">
      <h1>Artoo</h1>
      <p class="muted">Connect to Cloudflare R2</p>

      {#if error}
        <div class="error-banner">{error}</div>
      {/if}

      <label>
        <span>Account ID</span>
        <input
          type="text"
          bind:value={accountID}
          placeholder="Cloudflare Account ID"
        />
      </label>
      <label>
        <span>Access Key ID</span>
        <input
          type="text"
          bind:value={accessKeyID}
          placeholder="R2 Access Key ID"
        />
      </label>
      <label>
        <span>Secret Access Key</span>
        <input
          type="password"
          bind:value={secretAccessKey}
          placeholder="R2 Secret Access Key"
        />
      </label>
      <label>
        <span>Analytics API Token <span class="optional">(optional — enables instant bucket stats)</span></span>
        <input
          type="password"
          bind:value={apiToken}
          placeholder="Needs Account Analytics: Read permission"
        />
      </label>

      <button
        class="btn-primary full-width"
        on:click={handleLogin}
        disabled={loading || !accountID || !accessKeyID || !secretAccessKey}
      >
        {loading ? "Connecting..." : "Connect"}
      </button>
    </div>
  </div>
{:else if view === "browser"}
  <header>
    <div class="header-left">
      <strong>Artoo</strong>
      <button class="btn-ghost nav-btn" disabled={!canGoBack}    on:click={goBack}    title="Back (Alt+←)">←</button>
      <button class="btn-ghost nav-btn" disabled={!canGoForward} on:click={goForward} title="Forward (Alt+→)">→</button>
      {#if currentBucket}
        <button class="btn-ghost" on:click={backToBuckets}>Buckets</button>
        <span class="muted">/</span>
        <button class="btn-ghost breadcrumb" on:click={() => selectBucket(currentBucket)}>{currentBucket}</button>
        {#if currentPrefix}
          <span class="muted">/</span>
          {#each currentPrefix.split("/").filter(Boolean) as segment, i}
            <button
              class="btn-ghost breadcrumb"
              on:click={() => {
                const target = currentPrefix.split("/").filter(Boolean).slice(0, i + 1).join("/") + "/";
                pushNav();
                currentPrefix = target;
                loadObjects();
              }}>{segment}</button
            >
            {#if i < currentPrefix.split("/").filter(Boolean).length - 1}
              <span class="muted">/</span>
            {/if}
          {/each}
        {/if}
      {/if}
    </div>
    <div class="header-right">
      <button class="btn-ghost" on:click={openSettings} title="Settings">⚙</button>
    </div>
  </header>

  {#if error}
    <div class="error-banner">{error}</div>
  {/if}

  <main>
    {#if !currentBucket}
      <!-- Bucket list -->
      <div class="list">
        {#if loading}
          <p class="muted pad">Loading buckets...</p>
        {:else if buckets.length === 0}
          <p class="muted pad">No buckets found.</p>
        {:else}
          {#each buckets as bucket}
            <div class="list-row bucket-row">
              <button class="row-content" on:click={() => selectBucket(bucket.name)}>
                <span class="icon">📦</span>
                <span class="name">{bucket.name}</span>
                <span class="muted">{formatDate(bucket.creation_date)}</span>
              </button>
            </div>
            {#if bucketStats[bucket.name]}
              <div class="stats-panel">
                {#if bucketStats[bucket.name] === "loading"}
                  <span class="muted">Scanning…</span>
                {:else if bucketStats[bucket.name].error}
                  <span class="danger-text">{bucketStats[bucket.name].error}</span>
                {:else}
                  {@const s = bucketStats[bucket.name]}
                  <span class="stat-item"><span class="stat-label">Objects</span>{s.object_count.toLocaleString()}</span>
                  <span class="stat-sep">·</span>
                  <span class="stat-item"><span class="stat-label">Size</span>{formatSize(s.total_size)}</span>
                  {#if s.location}
                    <span class="stat-sep">·</span>
                    <span class="stat-item"><span class="stat-label">Region</span>{s.location}</span>
                  {/if}
                  {#if s.last_modified}
                    <span class="stat-sep">·</span>
                    <span class="stat-item"><span class="stat-label">Last modified</span>{formatDate(s.last_modified)}</span>
                  {/if}
                {/if}
              </div>
            {/if}
          {/each}
        {/if}
      </div>
    {:else}
      <!-- Object browser -->
      <div class="toolbar">
        <div class="toolbar-left">
          {#if currentPrefix}
            <button class="btn-ghost" on:click={navigateUp}>.. up</button>
          {/if}
          {#if objects.length + folders.length > 0}
            <button class="btn-ghost" on:click={selectAll}>
              {totalSelected > 0 ? "Deselect all" : "Select all"}
            </button>
          {/if}
          {#if expandedFolders.size > 0}
            <button class="btn-ghost" on:click={clearExpanded}>Collapse all</button>
          {/if}
          <div class="view-toggle">
            <button class="btn-ghost view-btn" class:view-btn-active={viewMode === "list"} title="List view" on:click={() => setViewMode("list")}>☰</button>
            <button class="btn-ghost view-btn" class:view-btn-active={viewMode === "grid"} title="Grid view" on:click={() => setViewMode("grid")}>⊞</button>
          </div>
          <div class="search-bar">
            <input
              class="search-input"
              type="text"
              placeholder="Search in {currentBucket}{currentPrefix ? '/' + currentPrefix : ''}…"
              bind:value={searchQuery}
              on:keydown={handleSearchKey}
              disabled={searching}
            />
            {#if searching}
              <span class="muted search-status">Searching…</span>
            {:else if searchResults !== null}
              <span class="muted search-status">{searchResults.length} result{searchResults.length !== 1 ? "s" : ""}</span>
              <button class="btn-ghost" on:click={clearSearch}>✕</button>
            {:else}
              <button class="btn-ghost" on:click={runSearch} disabled={!searchQuery.trim()}>Search</button>
            {/if}
          </div>
        </div>
        <div class="toolbar-right">
          {#if folderScanError}
            <span class="danger-text">{folderScanError}</span>
            <button class="btn-ghost" on:click={cancelFolderDownload}>Dismiss</button>
          {:else if folderScan === "scanning"}
            <span class="muted">Scanning folders…</span>
          {:else if folderScan && folderScan.warned}
            <span class="warn-text">
              {folderScan.object_count.toLocaleString()} objects · {formatSize(folderScan.total_size)} — large download, continue?
            </span>
            <button class="btn-ghost" on:click={() => runFolderDownload(folderScan.keys)}>Download</button>
            <button class="btn-ghost" on:click={cancelFolderDownload}>Cancel</button>
          {:else if totalSelected > 0}
            {#if confirmDelete}
              <span class="danger-text">
                Delete {totalSelected} item{totalSelected > 1 ? "s" : ""}?
              </span>
              <button class="btn-danger" on:click={handleDelete} disabled={deleting}>
                {deleting ? "Deleting..." : "Confirm"}
              </button>
              <button class="btn-ghost" on:click={cancelDelete}>Cancel</button>
            {:else}
              <span class="muted">{totalSelected} selected</span>
              <button class="btn-ghost" on:click={handleDownloadSelected}>
                Download ({totalSelected})
              </button>
              {#if deleteEnabled}
                <button class="btn-danger" on:click={handleDelete}>Delete</button>
              {/if}
            {/if}
          {/if}
        </div>
      </div>

      <div class={viewMode === "grid" && searchResults === null ? "grid-view" : "list"}>
        {#if searchResults !== null}
          <!-- Search results -->
          {#if searchResults.length === 0}
            <p class="muted pad">No results for "{searchQuery}".</p>
          {:else}
            {#each searchResults as obj}
              <div class="list-row" class:selected-row={selected.has(obj.key)}>
                <input type="checkbox" checked={selected.has(obj.key)} on:change={() => toggleSelect(obj.key)} />
                <div class="row-content file-row">
                  <span class="icon">📄</span>
                  <span class="name search-result-key" title={obj.key}>{obj.key}</span>
                  <span class="muted size">{formatSize(obj.size)}</span>
                  <span class="muted date">{formatDate(obj.last_modified)}</span>
                  <button class="btn-ghost row-action-btn" title="Preview" on:click|stopPropagation={() => openPreview(obj.key)}>👁</button>
                  <button class="btn-ghost row-action-btn" title="Download" on:click|stopPropagation={() => handleDownload(obj.key)}>↓</button>
                </div>
              </div>
            {/each}
          {/if}
        {:else if loading && objects.length === 0 && folders.length === 0}
          <p class="muted pad">Loading...</p>
        {:else if objects.length === 0 && folders.length === 0}
          <p class="muted pad">Empty.</p>
        {:else if viewMode === "grid"}
          <!-- Grid view -->
          {#each folders as prefix}
            <div
              class="grid-card"
              class:selected-row={selectedFolders.has(prefix)}
              on:click={() => navigateInto(prefix)}
            >
              <input
                type="checkbox"
                class="grid-check"
                checked={selectedFolders.has(prefix)}
                on:change|stopPropagation={() => toggleSelectFolder(prefix)}
                on:click|stopPropagation
              />
              <span class="grid-icon">📁</span>
              <span class="grid-name">{folderName(prefix)}</span>
            </div>
          {/each}
          {#each objects as obj}
            {@const thumb = thumbs[obj.key]}
            <div
              class="grid-card"
              class:selected-row={selected.has(obj.key)}
              on:click={() => openPreview(obj.key)}
            >
              <input
                type="checkbox"
                class="grid-check"
                checked={selected.has(obj.key)}
                on:change|stopPropagation={() => toggleSelect(obj.key)}
                on:click|stopPropagation
              />
              <div class="grid-thumb">
                {#if thumb && thumb !== "loading" && thumb !== "error"}
                  <img src={thumb} alt="" />
                {:else if isImage(obj.key)}
                  <span class="grid-icon">{thumb === "loading" ? "…" : "🖼"}</span>
                {:else}
                  <span class="grid-icon">📄</span>
                {/if}
              </div>
              <span class="grid-name" title={displayName(obj.key)}>{displayName(obj.key)}</span>
              <span class="grid-size muted">{formatSize(obj.size)}</span>
              <button
                class="btn-ghost grid-dl"
                title="Download"
                on:click|stopPropagation={() => handleDownload(obj.key)}
              >↓</button>
            </div>
          {/each}
          <!-- load more sits outside the grid -->
          {#if hasMore}
            <div class="grid-load-more">
              <button
                class="btn-ghost"
                on:click={() => loadObjects(continuationToken)}
                disabled={loading}
              >{loading ? "Loading..." : "Load more"}</button>
            </div>
          {/if}
        {:else}
          <!-- List view -->
          {#each flatItems as item, idx}
            {#if item.kind === "folder"}
              <div
                class="list-row"
                class:selected-row={item.depth === 0 && selectedFolders.has(item.prefix)}
                style="padding-left: {item.depth * 20}px"
              >
                {#if item.depth === 0}
                  <input
                    type="checkbox"
                    checked={selectedFolders.has(item.prefix)}
                    on:change={() => handleSelect(idx, item)}
                  />
                {:else}
                  <span class="depth-spacer" />
                {/if}
                <button
                  class="btn-ghost expand-btn"
                  title={expandedFolders.has(item.prefix) ? "Collapse" : "Expand"}
                  on:click|stopPropagation={() => toggleExpand(item.prefix)}
                >{expandedFolders.has(item.prefix) ? "▼" : "▶"}</button>
                <button class="row-content" on:click={() => navigateInto(item.prefix)}>
                  <span class="icon">📁</span>
                  <span class="name">{folderName(item.prefix)}</span>
                </button>
              </div>
            {:else if item.kind === "loading"}
              <div class="list-row expand-child" style="padding-left: {item.depth * 20 + 40}px">
                <span class="muted">Loading…</span>
              </div>
            {:else if item.kind === "error"}
              <div class="list-row expand-child" style="padding-left: {item.depth * 20 + 40}px">
                <span class="danger-text">{item.message}</span>
              </div>
            {:else if item.kind === "object"}
              <div
                class="list-row"
                class:selected-row={item.depth === 0 && selected.has(item.obj.key)}
                style="padding-left: {item.depth * 20}px"
              >
                {#if item.depth === 0}
                  <input
                    type="checkbox"
                    checked={selected.has(item.obj.key)}
                    on:change={() => handleSelect(idx, item)}
                  />
                {:else}
                  <span class="depth-spacer" />
                {/if}
                <div class="row-content file-row">
                  {#if inlinePreviews && isImage(item.obj.key)}
                    <span class="thumb-wrap">
                      {#if thumbs[item.obj.key] && thumbs[item.obj.key] !== "loading" && thumbs[item.obj.key] !== "error"}
                        <img class="thumb" src={thumbs[item.obj.key]} alt="" />
                      {:else if thumbs[item.obj.key] === "loading"}
                        <span class="thumb-placeholder">…</span>
                      {:else}
                        <span class="icon">🖼</span>
                      {/if}
                    </span>
                  {:else}
                    <span class="icon">📄</span>
                  {/if}
                  <span class="name">{item.depth > 0 ? item.obj.key.slice(item.obj.key.lastIndexOf("/", item.obj.key.length - 1) + 1) : displayName(item.obj.key)}</span>
                  <span class="muted size">{formatSize(item.obj.size)}</span>
                  <span class="muted date">{formatDate(item.obj.last_modified)}</span>
                  <button
                    class="btn-ghost row-action-btn"
                    title="Preview"
                    on:click|stopPropagation={() => openPreview(item.obj.key)}
                  >👁</button>
                  <button
                    class="btn-ghost row-action-btn"
                    title="Download"
                    on:click|stopPropagation={() => handleDownload(item.obj.key)}
                  >↓</button>
                </div>
              </div>
            {/if}
          {/each}
          {#if hasMore}
            <button
              class="btn-ghost full-width load-more"
              on:click={() => loadObjects(continuationToken)}
              disabled={loading}
            >
              {loading ? "Loading..." : "Load more"}
            </button>
          {/if}
        {/if}
      </div>
      
    {/if}
  </main>

  <!-- Preview modal -->
  {#if preview}
    <div class="modal-backdrop" on:click={closePreview}>
      <div class="modal" on:click|stopPropagation>
        <div class="modal-header">
          <span class="modal-title">{preview === "loading" ? "Loading…" : preview.key?.split("/").pop()}</span>
          <div class="modal-meta">
            {#if preview !== "loading" && preview.type !== "error"}
              <span class="muted">{preview.mime_type}</span>
              <span class="muted">·</span>
              <span class="muted">{formatSize(preview.size)}</span>
              {#if preview.truncated}
                <span class="warn-text">· preview truncated</span>
              {/if}
            {/if}
          </div>
          <button class="btn-ghost modal-close" on:click={closePreview}>✕</button>
        </div>

        <div class="modal-body">
          {#if preview === "loading"}
            <p class="muted center-text">Loading…</p>
          {:else if preview.type === "error"}
            <p class="danger-text">{preview.message}</p>
          {:else if preview.type === "image"}
            <div class="preview-image-wrap">
              <img src={preview.data_url} alt={preview.key} />
            </div>
          {:else if preview.type === "json"}
            <pre class="preview-code lang-json">{prettyJSON(preview.content)}</pre>
          {:else if preview.type === "csv"}
            {@const rows = parseCSV(preview.content)}
            <div class="preview-table-wrap">
              <table class="preview-table">
                <thead>
                  <tr>{#each rows[0] ?? [] as cell}<th>{cell}</th>{/each}</tr>
                </thead>
                <tbody>
                  {#each rows.slice(1) as row}
                    <tr>{#each row as cell}<td>{cell}</td>{/each}</tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {:else if preview.type === "text"}
            <pre class="preview-code">{preview.content}</pre>
          {:else}
            <div class="preview-unsupported">
              <p class="muted">No preview available for <strong>{preview.mime_type}</strong>.</p>
              <button class="btn-primary" on:click={() => { closePreview(); handleDownload(preview.key); }}>
                Download
              </button>
            </div>
          {/if}
        </div>
      </div>
    </div>
  {/if}

  <!-- Log panel -->
  {#if logs.length > 0}
    <div class="log-panel" class:log-collapsed={!logOpen}>
      <button class="log-header" on:click={() => (logOpen = !logOpen)}>
        <span>Log ({logs.length})</span>
        <span class="log-actions">
          {#if logOpen}
            <button class="btn-ghost log-btn" on:click|stopPropagation={clearLogs}>Clear</button>
          {/if}
          <span class="log-chevron">{logOpen ? "▼" : "▲"}</span>
        </span>
      </button>
      {#if logOpen}
        <div class="log-body" bind:this={logEl}>
          {#each logs as line}
            <div class="log-line">{line}</div>
          {/each}
        </div>
      {/if}
    </div>
  {/if}
{:else if view === "settings"}
  <header>
    <div class="header-left"><strong>Artoo</strong></div>
    <div class="header-right">
      <button class="btn-ghost" on:click={cancelSettings}>Cancel</button>
    </div>
  </header>
  <div class="settings-wrap">
    <div class="settings-card">
      <h2>Settings</h2>

      {#if settingsError}
        <div class="error-banner">{settingsError}</div>
      {/if}

      <section>
        <h3>Credentials</h3>
        <div class="field-grid">
          <span class="field-label">Account ID</span>
          <input type="text" bind:value={settingsAccountID} placeholder="Cloudflare Account ID" />

          <span class="field-label">Access Key ID</span>
          <input type="text" bind:value={settingsAccessKeyID} placeholder="R2 Access Key ID" />

          <span class="field-label">Secret Access Key</span>
          <input type="password" bind:value={settingsSecretAccessKey} placeholder="••••••••" />

          <span class="field-label">
            Analytics Token
            <span class="optional">Account Analytics: Read</span>
          </span>
          <input type="password" bind:value={settingsApiToken} placeholder="cfat_… (optional)" />
        </div>
      </section>

      <section>
        <h3>Downloads</h3>
        <div class="field-grid">
          <span class="field-label">
            Download concurrency
            <span class="optional">1–{dlMaxConcurrency} parallel</span>
          </span>
          <input
            class="input-narrow"
            type="number"
            min="1"
            max={dlMaxConcurrency}
            bind:value={settingsConcurrency}
          />
        </div>
      </section>

      <section>
        <h3>Display</h3>
        <div class="field-grid">
          <span class="field-label">
            Inline image previews
            <span class="optional">loads thumbnails per row</span>
          </span>
          <label class="toggle">
            <input type="checkbox" bind:checked={settingsInlinePreviews} />
            <span class="toggle-label">{settingsInlinePreviews ? "On" : "Off"}</span>
          </label>

          <span class="field-label">
            Image preview cap
            <span class="optional">limits R2 Class B cost per thumbnail</span>
          </span>
          <div>
            <select bind:value={settingsPreviewSizeLimit}>
              <option value={131072}>128 KB</option>
              <option value={262144}>256 KB</option>
              <option value={524288}>512 KB</option>
              <option value={1048576}>1 MB</option>
              <option value={2097152}>2 MB</option>
              <option value={5242880}>5 MB</option>
              <option value={10485760}>10 MB</option>
            </select>
          </div>
        </div>
      </section>

      <section>
        <h3>Safety</h3>
        <div class="field-grid">
          <span class="field-label">
            Allow delete
            <span class="optional">shows delete buttons and enables delete operations</span>
          </span>
          <label class="toggle">
            <input type="checkbox" bind:checked={settingsDeleteEnabled} />
            <span class="toggle-label">{settingsDeleteEnabled ? "Enabled" : "Locked"}</span>
          </label>
        </div>
      </section>

      <div class="settings-actions">
        <button
          class="btn-primary"
          on:click={saveSettings}
          disabled={settingsSaving || !settingsAccountID || !settingsAccessKeyID || !settingsSecretAccessKey}
        >
          {settingsSaving ? "Saving…" : "Save"}
        </button>
        <button class="btn-ghost" on:click={cancelSettings}>Cancel</button>
      </div>

      <section class="disconnect-section">
        <h3>Account</h3>
        {#if confirmDisconnect}
          <div class="field-grid">
            <span class="field-label danger-text">Clear all credentials?</span>
            <div style="display:flex;gap:8px">
              <button class="btn-danger" on:click={handleLogout}>Disconnect</button>
              <button class="btn-ghost" on:click={() => confirmDisconnect = false}>Cancel</button>
            </div>
          </div>
        {:else}
          <div class="field-grid">
            <span class="field-label">
              Disconnect
              <span class="optional">clears saved credentials from disk</span>
            </span>
            <button class="btn-ghost disconnect-btn" on:click={() => confirmDisconnect = true}>
              Disconnect…
            </button>
          </div>
        {/if}
      </section>
    </div>
  </div>
{/if}

<style>
  .center-screen {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 100vh;
  }

  .login-card {
    width: 360px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .settings-wrap {
    display: flex;
    justify-content: center;
    padding: 32px 16px;
    overflow-y: auto;
    height: calc(100vh - var(--header-height));
  }

  .settings-card {
    width: 520px;
    display: flex;
    flex-direction: column;
    gap: 24px;
  }

  .settings-card h2 {
    margin: 0;
    font-size: 16px;
  }

  .settings-card h3 {
    margin: 0 0 12px;
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.07em;
    color: var(--text-muted);
  }

  .settings-card section {
    padding-bottom: 24px;
    border-bottom: 1px solid var(--border);
  }

  .settings-card section:last-of-type {
    border-bottom: none;
    padding-bottom: 0;
  }

  .field-grid {
    display: grid;
    grid-template-columns: 160px 1fr;
    align-items: center;
    row-gap: 10px;
    column-gap: 16px;
  }

  .field-label {
    font-size: 13px;
    text-align: right;
    color: var(--text-muted);
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 2px;
    line-height: 1.3;
  }

  .field-grid input {
    width: 100%;
    box-sizing: border-box;
  }

  .input-narrow {
    width: 80px !important;
  }

  .settings-card select {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text);
    padding: 4px 8px;
    font-size: 13px;
    cursor: pointer;
    color-scheme: dark;
  }

  .settings-actions {
    display: flex;
    gap: 8px;
  }

  .disconnect-section {
    border-top: 1px solid var(--border);
    padding-top: 20px;
  }

  .disconnect-btn {
    color: var(--danger);
  }
  .disconnect-btn:hover {
    background: rgba(239, 68, 68, 0.1);
    color: var(--danger);
  }

  .login-card h1 {
    font-size: 24px;
    font-weight: 700;
  }

  .login-card label {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .login-card label span {
    font-size: 12px;
    color: var(--text-muted);
  }

  .optional {
    font-style: italic;
    opacity: 0.7;
  }

  .login-card input {
    width: 100%;
  }

  .full-width {
    width: 100%;
  }

  .muted {
    color: var(--text-muted);
  }

  .pad {
    padding: 20px;
    text-align: center;
  }

  .error-banner {
    background: rgba(239, 68, 68, 0.15);
    color: var(--danger);
    border: 1px solid var(--danger);
    border-radius: 6px;
    padding: 8px 12px;
    font-size: 13px;
    margin: 8px 16px;
  }

  .danger-text {
    color: var(--danger);
    font-size: 13px;
  }

  .warn-text {
    color: var(--warning, #e8a020);
    font-size: 13px;
  }

  /* Header */
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 8px 20px;
    border-bottom: 1px solid rgba(255, 255, 255, 0.08);
    gap: 8px;
    min-height: 44px;
    --wails-draggable: drag;
  }

  .header-left {
    display: flex;
    align-items: center;
    gap: 4px;
    flex-wrap: wrap;
  }

  .header-right {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-shrink: 0;
  }


  .breadcrumb {
    padding: 2px 4px;
    font-size: 13px;
  }

  .nav-btn {
    padding: 2px 7px;
    font-size: 14px;
  }
  .nav-btn:disabled {
    opacity: 0.25;
  }

  /* Toolbar */
  .toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0 20px;
    border-bottom: 1px solid rgba(255, 255, 255, 0.08);
    gap: 8px;
    height: 38px;
    flex-shrink: 0;
  }

  .toolbar-left,
  .toolbar-right {
    display: flex;
    align-items: center;
    gap: 4px;
  }

  /* List */
  main {
    flex: 1;
    overflow-y: auto;
  }

  .list {
    display: flex;
    flex-direction: column;
    padding: 6px 12px;
    gap: 1px;
  }

  .list-row {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 0 10px;
    height: 30px;
    border-radius: 6px;
    border: none;
    background: none;
    color: var(--text);
    text-align: left;
    font-size: 13px;
    transition: background 0.1s;
    flex-shrink: 0;
  }

  .list-row:hover {
    background: var(--surface);
  }

  .selected-row {
    background: rgba(249, 115, 22, 0.1);
  }
  .selected-row:hover {
    background: rgba(249, 115, 22, 0.16);
  }

  .row-content {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 1;
    min-width: 0;
    height: 100%;
    background: none;
    border: none;
    color: var(--text);
    font-size: 13px;
    text-align: left;
    cursor: pointer;
    padding: 0;
    border-radius: 0;
  }

  .file-row {
    cursor: default;
  }

  .icon {
    font-size: 14px;
    line-height: 1;
    flex-shrink: 0;
    width: 18px;
    text-align: center;
  }

  .name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .row-action-btn {
    flex-shrink: 0;
    padding: 1px 5px;
    font-size: 12px;
    line-height: 1;
  }

  .thumb-wrap {
    flex-shrink: 0;
    width: 22px;
    height: 22px;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .thumb {
    width: 22px;
    height: 22px;
    object-fit: cover;
    border-radius: 2px;
    border: 1px solid var(--border);
  }

  .thumb-placeholder {
    font-size: 11px;
    color: var(--text-muted);
  }

  .toggle {
    display: flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
  }

  .toggle-label {
    font-size: 13px;
  }

  /* Modal */
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.6);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 100;
  }

  .modal {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    display: flex;
    flex-direction: column;
    width: min(90vw, 900px);
    max-height: 85vh;
    overflow: hidden;
  }

  .modal-header {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 14px;
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }

  .modal-title {
    font-weight: 600;
    font-size: 13px;
    flex-shrink: 0;
  }

  .modal-meta {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    flex: 1;
  }

  .modal-close {
    flex-shrink: 0;
    font-size: 14px;
    padding: 2px 6px;
  }

  .modal-body {
    overflow: auto;
    flex: 1;
    min-height: 0;
  }

  .preview-image-wrap {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 16px;
    min-height: 200px;
  }

  .preview-image-wrap img {
    max-width: 100%;
    max-height: calc(85vh - 80px);
    object-fit: contain;
  }

  .preview-code {
    margin: 0;
    padding: 14px;
    font-size: 12px;
    font-family: monospace;
    line-height: 1.5;
    white-space: pre-wrap;
    word-break: break-all;
    tab-size: 2;
  }

  .preview-table-wrap {
    overflow: auto;
    max-height: calc(85vh - 80px);
  }

  .preview-table {
    border-collapse: collapse;
    font-size: 12px;
    width: 100%;
  }

  .preview-table th,
  .preview-table td {
    padding: 5px 10px;
    border: 1px solid var(--border);
    text-align: left;
    white-space: nowrap;
  }

  .preview-table th {
    background: var(--surface);
    font-weight: 600;
    position: sticky;
    top: 0;
  }

  .preview-table tr:hover td {
    background: var(--surface);
  }

  .preview-unsupported {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 12px;
    padding: 32px;
    min-height: 150px;
  }

  .center-text {
    text-align: center;
    padding: 32px;
  }

  .bucket-row {
    cursor: default;
  }

  /* Grid view */
  .grid-view {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
    gap: 12px;
    padding: 16px;
    align-content: start;
  }

  .grid-card {
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 6px;
    padding: 12px 8px 10px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--surface);
    cursor: pointer;
    transition: border-color 0.1s, background 0.1s;
    overflow: hidden;
  }

  .grid-card:hover {
    border-color: var(--text-muted);
    background: var(--bg);
  }

  .grid-card.selected-row {
    border-color: #f97316;
    background: rgba(249, 115, 22, 0.08);
  }

  .grid-check {
    position: absolute;
    top: 6px;
    left: 6px;
  }

  .grid-thumb {
    width: 80px;
    height: 80px;
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
  }

  .grid-thumb img {
    width: 80px;
    height: 80px;
    object-fit: cover;
    border-radius: 4px;
  }

  .grid-icon {
    font-size: 48px;
    line-height: 1;
  }

  .grid-name {
    font-size: 11px;
    text-align: center;
    word-break: break-all;
    line-height: 1.3;
    max-width: 100%;
    overflow: hidden;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
  }

  .grid-size {
    font-size: 10px;
  }

  .grid-dl {
    font-size: 11px;
    padding: 2px 8px;
    opacity: 0;
    transition: opacity 0.1s;
  }

  .grid-card:hover .grid-dl {
    opacity: 1;
  }

  .grid-load-more {
    grid-column: 1 / -1;
    display: flex;
    justify-content: center;
    padding: 8px 0;
  }

  .search-bar {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-left: 8px;
    padding-left: 12px;
    border-left: 1px solid var(--border);
  }

  .search-input {
    width: 240px;
    height: 24px;
    padding: 0 8px;
    font-size: 12px;
    border-radius: 4px;
    box-sizing: border-box;
  }

  .search-status {
    font-size: 12px;
    white-space: nowrap;
  }

  .search-result-key {
    font-family: monospace;
    font-size: 12px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .view-toggle {
    display: flex;
    gap: 2px;
  }

  .view-btn {
    padding: 2px 6px;
    font-size: 14px;
  }

  .view-btn-active {
    color: var(--text);
    background: var(--surface);
  }

  .expand-btn {
    flex-shrink: 0;
    width: 20px;
    padding: 0;
    font-size: 9px;
    color: var(--text-muted);
    line-height: 1;
    text-align: center;
  }

  .depth-spacer {
    display: inline-block;
    width: 20px;
    flex-shrink: 0;
  }

  .expand-child {
    font-size: 13px;
    color: var(--text-muted);
  }

  .stats-panel {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 4px 10px 4px 42px;
    font-size: 12px;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    flex-wrap: wrap;
  }

  .stat-item {
    display: flex;
    gap: 4px;
  }

  .stat-label {
    color: var(--text-muted);
  }

  .stat-sep {
    color: var(--text-muted);
  }

  .icon {
    flex-shrink: 0;
    width: 20px;
    text-align: center;
  }

  .name {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .size {
    width: 80px;
    text-align: right;
    flex-shrink: 0;
  }

  .date {
    width: 160px;
    text-align: right;
    flex-shrink: 0;
  }

  input[type="checkbox"] {
    width: 16px;
    height: 16px;
    accent-color: var(--accent);
    flex-shrink: 0;
    cursor: pointer;
  }

  .load-more {
    padding: 12px;
    border-radius: 0;
  }

  /* Log panel */
  .log-panel {
    border-top: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    flex-shrink: 0;
    max-height: 200px;
  }

  .log-collapsed {
    max-height: none;
  }

  .log-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 4px 20px;
    background: var(--surface);
    border: none;
    border-radius: 0;
    color: var(--text-muted);
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;
    user-select: none;
  }

  .log-header:hover {
    background: var(--surface-hover);
  }

  .log-actions {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .log-btn {
    font-size: 11px;
    padding: 2px 6px;
  }

  .log-chevron {
    font-size: 10px;
  }

  .log-body {
    overflow-y: auto;
    flex: 1;
    padding: 4px 0;
    background: var(--bg);
    font-family: "SF Mono", "Fira Code", "Cascadia Code", monospace;
    font-size: 12px;
    max-height: 160px;
  }

  .log-line {
    padding: 1px 20px;
    color: var(--text-muted);
    white-space: pre-wrap;
    word-break: break-all;
  }

  .log-line:hover {
    background: var(--surface);
  }
</style>
