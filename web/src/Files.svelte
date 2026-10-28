<script>
  // File explorer in the Finder / Explorer layout: places + folder tree on the left, a toolbar
  // with history and an editable path, list or icon view, multi-select, keyboard, context menu,
  // drag and drop (files and whole folders from the desktop, items onto folders) and an upload panel.
  import { onMount, tick } from 'svelte';
  import { SvelteSet } from 'svelte/reactivity';
  import { api } from '$lib/api.js';
  import { toast } from '$lib/toast.svelte.js';
  import { bytes, pct } from '$lib/format.js';
  import { kindOf, PREVIEW_IMAGE, PREVIEW_TEXT } from '$lib/fileKinds.js';
  import Dialog from '$lib/Dialog.svelte';
  import ContextMenu from '$lib/ContextMenu.svelte';
  import ShareLinkDialog from '$lib/ShareLinkDialog.svelte';
  import LinkIcon from '@lucide/svelte/icons/link';
  import FileTree from '$lib/FileTree.svelte';
  import ChevronLeft from '@lucide/svelte/icons/chevron-left';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import ArrowUp from '@lucide/svelte/icons/arrow-up';
  import ArrowDown from '@lucide/svelte/icons/arrow-down';
  import HardDrive from '@lucide/svelte/icons/hard-drive';
  import Server from '@lucide/svelte/icons/server';
  import Folder from '@lucide/svelte/icons/folder';
  import FolderPlus from '@lucide/svelte/icons/folder-plus';
  import FolderUp from '@lucide/svelte/icons/folder-up';
  import Upload from '@lucide/svelte/icons/upload';
  import Download from '@lucide/svelte/icons/download';
  import Pencil from '@lucide/svelte/icons/pencil';
  import Trash from '@lucide/svelte/icons/trash-2';
  import Copy from '@lucide/svelte/icons/copy';
  import Scissors from '@lucide/svelte/icons/scissors';
  import ClipboardPaste from '@lucide/svelte/icons/clipboard-paste';
  import FolderInput from '@lucide/svelte/icons/folder-input';
  import Eye from '@lucide/svelte/icons/eye';
  import Info from '@lucide/svelte/icons/info';
  import Search from '@lucide/svelte/icons/search';
  import List from '@lucide/svelte/icons/list';
  import LayoutGrid from '@lucide/svelte/icons/layout-grid';
  import RefreshCw from '@lucide/svelte/icons/refresh-cw';
  import PanelLeft from '@lucide/svelte/icons/panel-left';
  import Loader from '@lucide/svelte/icons/loader-circle';
  import CircleAlert from '@lucide/svelte/icons/circle-alert';
  import CircleCheck from '@lucide/svelte/icons/circle-check';
  import RotateCcw from '@lucide/svelte/icons/rotate-ccw';
  import X from '@lucide/svelte/icons/x';

  let { onSessionLost } = $props();

  const CHUNK = 8 * 1024 * 1024;
  const PARALLEL = 2;
  const DRAG_TYPE = 'application/x-nxbox-paths';

  let places = $state([]);
  let path = $state(''); // '' = the server overview (all places)
  let entries = $state(null);
  let error = $state('');
  let back = $state([]), fwd = $state([]);
  let view = $state(stored('nxbox-files-view', 'list'));
  let sort = $state({ key: 'name', dir: 1 });
  let query = $state('');
  let selected = new SvelteSet();
  let anchor = -1;
  let clip = $state(null); // { mode: 'copy' | 'cut', paths: [] }
  let editing = $state(false), pathText = $state('');
  let menu = $state(null);
  let dropOn = $state(''); // folder path highlighted as a drop target
  let dragOver = $state(false);
  let sideOpen = $state(false);
  let uploads = $state([]);
  let panelOpen = $state(true);
  let listEl = $state(), pathInput = $state(), fileInput = $state(), folderInput = $state();

  let nameOpen = $state(false), nameMode = $state('mkdir'), nameValue = $state(''), nameFor = $state(null), nameBusy = $state(false), nameError = $state('');
  let moveOpen = $state(false), moveTo = $state(''), moveBusy = $state(false), moveError = $state('');
  let delOpen = $state(false), delList = $state([]), delBusy = $state(false);
  let viewOpen = $state(false), viewFor = $state(null), viewText = $state('');
  let infoOpen = $state(false), infoFor = $state(null), infoSize = $state(null);
  let replaceOpen = $state(false), replaceJobs = $state([]);

  function stored(k, d) { try { return localStorage.getItem(k) || d; } catch { return d; } }
  $effect(() => { try { localStorage.setItem('nxbox-files-view', view); } catch { /* blocked */ } });

  const join = (dir, name) => (dir.endsWith('/') ? dir : dir + '/') + name;
  const parentOf = (p) => p.slice(0, p.lastIndexOf('/')) || '/';
  const baseOf = (p) => p.slice(p.lastIndexOf('/') + 1);
  const fileURL = (p, inline) => `/api/files/download?path=${encodeURIComponent(p)}${inline ? '&inline=1' : ''}`;
  const within = (p, r) => p === r || p.startsWith(r.endsWith('/') ? r : r + '/');

  const root = $derived(places.map((p) => p.path).filter((r) => path && within(path, r)).sort((a, b) => b.length - a.length)[0] || '');
  const crumbs = $derived.by(() => {
    if (!root) return [];
    const out = [{ label: root, path: root }];
    let cur = root;
    for (const part of path.slice(root.length).split('/').filter(Boolean)) {
      cur = join(cur, part);
      out.push({ label: part, path: cur });
    }
    return out;
  });

  const shown = $derived.by(() => {
    if (!entries) return [];
    const q = query.trim().toLowerCase();
    const list = q ? entries.filter((e) => e.name.toLowerCase().includes(q)) : [...entries];
    const { key, dir } = sort;
    const val = (e) => (key === 'size' ? (e.dir ? -1 : e.size) : key === 'mtime' ? e.mtime : key === 'kind' ? kindOf(e).label.toLowerCase() : e.name.toLowerCase());
    return list.sort((a, b) => (a.dir !== b.dir ? (a.dir ? -1 : 1) : val(a) < val(b) ? -dir : val(a) > val(b) ? dir : a.name.localeCompare(b.name)));
  });
  const selEntries = $derived(shown.filter((e) => selected.has(e.name)));
  const selSize = $derived(selEntries.reduce((s, e) => s + (e.dir ? 0 : e.size), 0));
  const activeUploads = $derived(uploads.filter((u) => u.status === 'queued' || u.status === 'uploading').length);

  function lost(res) { if (res.status === 401) { onSessionLost(); return true; } return false; }

  async function loadPlaces() {
    const res = await api('/api/files?path=');
    if (lost(res)) return;
    if (res.ok) places = res.data.places || res.data.roots.map((p) => ({ path: p }));
    else error = res.error;
  }

  // open navigates; history: 'push' (normal), 'none' (back/forward/reload).
  async function open(p, history = 'push') {
    if (p === '') {
      if (history === 'push' && path !== '') { back = [...back, path]; fwd = []; }
      path = '';
      entries = null;
      error = '';
      selected.clear();
      return loadPlaces();
    }
    const res = await api(`/api/files?path=${encodeURIComponent(p)}`);
    if (lost(res)) return;
    if (!res.ok) {
      toast(res.error, 'error', 6000);
      return false;
    }
    if (history === 'push' && p !== path) { back = [...back, path]; fwd = []; }
    if (p !== path) { selected.clear(); query = ''; anchor = -1; }
    path = p;
    entries = res.data.entries;
    error = '';
    sideOpen = false;
    return true;
  }
  const reload = () => open(path, 'none');
  function goBack() { if (!back.length) return; fwd = [path, ...fwd]; const p = back.at(-1); back = back.slice(0, -1); open(p, 'none'); }
  function goFwd() { if (!fwd.length) return; back = [...back, path]; const p = fwd[0]; fwd = fwd.slice(1); open(p, 'none'); }
  function goUp() { if (path) open(path === root ? '' : parentOf(path)); }

  // Path bar: breadcrumbs, a click on free space turns it into a text field.
  async function editPath() {
    pathText = path || '/';
    editing = true;
    await tick();
    pathInput?.select();
  }
  async function submitPath(e) {
    e.preventDefault();
    let p = pathText.trim().replace(/\/+$/, '') || '/';
    if (!p.startsWith('/')) p = join(path || '/', p);
    if (await open(p)) editing = false;
    else pathInput?.select();
  }

  // Selection: click selects one, Cmd/Ctrl toggles, Shift selects a range from the anchor.
  function select(e, entry, i) {
    if (e.shiftKey && anchor >= 0) {
      if (!(e.metaKey || e.ctrlKey)) selected.clear();
      const [a, b] = [Math.min(anchor, i), Math.max(anchor, i)];
      for (let k = a; k <= b; k++) selected.add(shown[k].name);
    } else if (e.metaKey || e.ctrlKey) {
      selected.has(entry.name) ? selected.delete(entry.name) : selected.add(entry.name);
      anchor = i;
    } else {
      selected.clear();
      selected.add(entry.name);
      anchor = i;
    }
  }
  function focusRow(i) {
    tick().then(() => listEl?.querySelector(`[data-i="${i}"]`)?.scrollIntoView({ block: 'nearest' }));
  }
  function gridCols() {
    const items = listEl?.querySelectorAll('[data-i]');
    if (!items?.length || view !== 'grid') return 1;
    const top = items[0].offsetTop;
    let n = 0;
    for (const it of items) { if (it.offsetTop !== top) break; n++; }
    return Math.max(1, n);
  }

  function keydown(e) {
    if (!path || nameOpen || moveOpen || delOpen || viewOpen || infoOpen || menu || e.target.closest('input, textarea')) return;
    const mod = e.metaKey || e.ctrlKey;
    const cur = anchor >= 0 ? anchor : -1;
    const step = { ArrowDown: view === 'grid' ? gridCols() : 1, ArrowUp: view === 'grid' ? -gridCols() : -1, ArrowRight: view === 'grid' ? 1 : 0, ArrowLeft: view === 'grid' ? -1 : 0 }[e.key];
    if (step !== undefined) {
      if (!step || !shown.length) return;
      e.preventDefault();
      const i = Math.min(shown.length - 1, Math.max(0, cur < 0 ? 0 : cur + step));
      if (e.shiftKey) { selected.add(shown[i].name); } else { selected.clear(); selected.add(shown[i].name); }
      anchor = i;
      focusRow(i);
    } else if (e.key === 'Enter' && selEntries.length === 1) {
      e.preventDefault();
      activate(selEntries[0]);
    } else if (e.key === 'Backspace' && !mod) {
      e.preventDefault();
      goUp();
    } else if (e.key === 'Delete' || (e.key === 'Backspace' && mod)) {
      if (selEntries.length) { e.preventDefault(); askDelete(selEntries); }
    } else if (e.key === 'F2' && selEntries.length === 1) {
      e.preventDefault();
      askName('rename', selEntries[0]);
    } else if (mod && e.key.toLowerCase() === 'a') {
      e.preventDefault();
      shown.forEach((s) => selected.add(s.name));
    } else if (mod && e.key.toLowerCase() === 'c' && selEntries.length) {
      setClip('copy');
    } else if (mod && e.key.toLowerCase() === 'x' && selEntries.length) {
      setClip('cut');
    } else if (mod && e.key.toLowerCase() === 'v' && clip) {
      paste(path);
    } else if (e.key === 'Escape') {
      selected.clear();
    }
  }

  async function activate(e) {
    const p = join(path, e.name);
    if (e.dir) return open(p);
    if (PREVIEW_IMAGE.test(e.name) || PREVIEW_TEXT.test(e.name)) return preview(e);
    download([e]);
  }

  async function preview(e) {
    const p = join(path, e.name);
    viewFor = { ...e, path: p };
    viewText = '';
    viewOpen = true;
    if (PREVIEW_TEXT.test(e.name)) {
      if (e.size > 2 * 1024 * 1024) viewText = 'This file is too large to show here. Download it instead.';
      else {
        const r = await fetch(fileURL(p, true), { credentials: 'same-origin' }).catch(() => null);
        viewText = r && r.ok ? await r.text() : 'The file could not be read.';
      }
    }
  }

  function download(list) {
    if (!list.length) return;
    const a = document.createElement('a');
    a.href = list.length === 1 && !list[0].dir
      ? fileURL(join(path, list[0].name), false)
      : '/api/files/zip?' + list.map((e) => 'path=' + encodeURIComponent(join(path, e.name))).join('&');
    document.body.appendChild(a);
    a.click();
    a.remove();
    if (list.length > 1 || list[0].dir) toast('Your download is being packed as a zip.', 'info');
  }

  function setClip(mode) {
    clip = { mode, paths: selEntries.map((e) => join(path, e.name)) };
    toast(`${clip.paths.length === 1 ? baseOf(clip.paths[0]) : `${clip.paths.length} items`} ${mode === 'copy' ? 'copied' : 'cut'}. Paste them in another folder.`, 'info');
  }

  // A free name for a copy in the same folder: "name copy", "name copy 2".
  function freeName(name, taken) {
    if (!taken.has(name)) return name;
    const dot = name.lastIndexOf('.');
    const [stem, ext] = dot > 0 ? [name.slice(0, dot), name.slice(dot)] : [name, ''];
    for (let n = 1; ; n++) {
      const c = `${stem} copy${n > 1 ? ' ' + n : ''}${ext}`;
      if (!taken.has(c)) return c;
    }
  }

  async function paste(dest) {
    if (!clip) return;
    const { mode, paths } = clip;
    let taken = new Set(dest === path ? (entries || []).map((e) => e.name) : []);
    if (dest !== path) {
      const res = await api(`/api/files?path=${encodeURIComponent(dest)}`);
      if (res.ok) taken = new Set(res.data.entries.map((e) => e.name));
    }
    let done = 0;
    for (const p of paths) {
      if (mode === 'cut' && parentOf(p) === dest) continue;
      const name = mode === 'copy' ? freeName(baseOf(p), taken) : baseOf(p);
      taken.add(name);
      const res = await api(`/api/files/${mode === 'copy' ? 'copy' : 'rename'}`, { method: 'POST', body: { path: p, to: join(dest, name) } });
      if (lost(res)) return;
      if (!res.ok) { toast(`${baseOf(p)}: ${res.error}`, 'error', 8000); continue; }
      done++;
    }
    if (mode === 'cut') clip = null;
    if (done) toast(`${done === 1 ? '1 item' : `${done} items`} ${mode === 'copy' ? 'copied' : 'moved'}.`, 'success');
    reload();
  }

  async function moveMany(paths, dest) {
    let done = 0;
    for (const p of paths) {
      if (parentOf(p) === dest || p === dest || dest.startsWith(p + '/')) continue;
      const res = await api('/api/files/rename', { method: 'POST', body: { path: p, to: join(dest, baseOf(p)) } });
      if (lost(res)) return false;
      if (!res.ok) { toast(`${baseOf(p)}: ${res.error}`, 'error', 8000); continue; }
      done++;
    }
    if (done) toast(`${done === 1 ? '1 item' : `${done} items`} moved to ${baseOf(dest) || dest}.`, 'success');
    reload();
    return true;
  }

  function askName(mode, entry) {
    nameMode = mode;
    nameFor = entry;
    nameValue = mode === 'mkdir' ? 'New folder' : entry.name;
    nameError = '';
    nameOpen = true;
  }
  async function submitName(e) {
    e.preventDefault();
    const name = nameValue.trim();
    nameBusy = true;
    nameError = '';
    const res = nameMode === 'mkdir'
      ? await api('/api/files/mkdir', { method: 'POST', body: { path: join(path, name) } })
      : await api('/api/files/rename', { method: 'POST', body: { path: join(path, nameFor.name), to: join(path, name) } });
    nameBusy = false;
    if (lost(res)) return;
    if (!res.ok) { nameError = res.error; return; }
    nameOpen = false;
    await reload();
    selected.clear();
    selected.add(name);
  }

  function askMove(list) {
    moveTo = path;
    moveError = '';
    delList = list;
    moveOpen = true;
  }
  async function submitMove(e) {
    e.preventDefault();
    moveBusy = true;
    const dest = moveTo.trim().replace(/\/+$/, '') || '/';
    const check = await api(`/api/files?path=${encodeURIComponent(dest)}`);
    if (!check.ok) { moveBusy = false; moveError = check.error; return; }
    await moveMany(delList.map((x) => join(path, x.name)), dest);
    moveBusy = false;
    moveOpen = false;
  }

  function askDelete(list) { delList = list; delOpen = true; }
  async function doDelete() {
    delBusy = true;
    for (const x of delList) {
      const res = await api('/api/files/delete', { method: 'POST', body: { path: join(path, x.name) } });
      if (lost(res)) return;
      if (!res.ok) toast(`${x.name}: ${res.error}`, 'error', 8000);
    }
    delBusy = false;
    delOpen = false;
    toast(delList.length === 1 ? `${delList[0].name} was deleted.` : `${delList.length} items were deleted.`, 'success');
    selected.clear();
    reload();
  }

  async function showInfo(e) {
    infoFor = { ...e, path: join(path, e.name), kind: kindOf(e).label };
    infoSize = null;
    infoOpen = true;
    if (e.dir) {
      const res = await api(`/api/files/size?path=${encodeURIComponent(infoFor.path)}`);
      infoSize = res.ok ? res.data : { error: res.error };
    }
  }

  // Context menu: on an item (acts on the selection) or on empty space (the folder).
  let share = $state({ path: '', name: '', dir: false, origin: null }), shareOpen = $state(false), menuAt = null;
  function context(e, entry, i) {
    e.preventDefault();
    if (!path) return;
    if (entry && !selected.has(entry.name)) { selected.clear(); selected.add(entry.name); anchor = i; }
    if (!entry) selected.clear();
    const one = entry && selEntries.length === 1 ? selEntries[0] : null;
    const items = entry
      ? [
          { key: 'open', label: one?.dir || !one ? 'Open' : 'Preview', icon: one?.dir ? Folder : Eye, disabled: !one },
          { key: 'download', label: selEntries.length > 1 || entry.dir ? 'Download as zip' : 'Download', icon: Download },
          'sep',
          { key: 'rename', label: 'Rename', icon: Pencil, hint: 'F2', disabled: !one },
          { key: 'copy', label: 'Copy', icon: Copy, hint: shortcut('C') },
          { key: 'cut', label: 'Cut', icon: Scissors, hint: shortcut('X') },
          { key: 'paste-into', label: 'Paste into folder', icon: ClipboardPaste, disabled: !clip || !one?.dir },
          { key: 'move', label: 'Move to', icon: FolderInput },
          { key: 'share', label: 'Share link', icon: LinkIcon, disabled: !one },
          'sep',
          { key: 'info', label: 'Get info', icon: Info, disabled: !one },
          { key: 'delete', label: 'Delete', icon: Trash, danger: true, hint: 'Del' },
        ]
      : [
          { key: 'mkdir', label: 'New folder', icon: FolderPlus },
          { key: 'upload', label: 'Upload files', icon: Upload },
          { key: 'upload-folder', label: 'Upload a folder', icon: FolderUp },
          'sep',
          { key: 'paste', label: 'Paste', icon: ClipboardPaste, hint: shortcut('V'), disabled: !clip },
          { key: 'download-folder', label: 'Download this folder', icon: Download },
          { key: 'reload', label: 'Reload', icon: RefreshCw },
        ];
    menu = { x: e.clientX, y: e.clientY, items };
    menuAt = { x: e.clientX, y: e.clientY };
  }
  const shortcut = (k) => (navigator.platform?.startsWith('Mac') ? `⌘${k}` : `Ctrl+${k}`);

  function pick(key) {
    const one = selEntries.length === 1 ? selEntries[0] : null;
    ({
      open: () => one && activate(one),
      download: () => download(selEntries),
      rename: () => one && askName('rename', one),
      copy: () => setClip('copy'),
      cut: () => setClip('cut'),
      'paste-into': () => one && paste(join(path, one.name)),
      move: () => askMove(selEntries),
      info: () => one && showInfo(one),
      share: () => one && (share = { path: join(path, one.name), name: one.name, dir: one.dir, origin: menuAt }, shareOpen = true),
      delete: () => askDelete(selEntries),
      mkdir: () => askName('mkdir'),
      upload: () => fileInput.click(),
      'upload-folder': () => folderInput.click(),
      paste: () => paste(path),
      'download-folder': downloadFolder,
      reload,
    })[key]?.();
  }
  // "Download this folder" zips the current folder itself.
  function downloadFolder() {
    const a = document.createElement('a');
    a.href = '/api/files/zip?path=' + encodeURIComponent(path);
    document.body.appendChild(a);
    a.click();
    a.remove();
    toast('Your download is being packed as a zip.', 'info');
  }

  // Drag items (move) and drop files or folders from the desktop (upload).
  function dragStart(e, entry, i) {
    if (!selected.has(entry.name)) { selected.clear(); selected.add(entry.name); anchor = i; }
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData(DRAG_TYPE, JSON.stringify(selEntries.map((x) => join(path, x.name))));
  }
  function acceptsDrop(e) {
    const t = e.dataTransfer.types;
    return t.includes(DRAG_TYPE) || t.includes('Files');
  }
  function overFolder(e, folder) {
    if (!acceptsDrop(e)) return;
    e.preventDefault();
    e.stopPropagation();
    e.dataTransfer.dropEffect = e.dataTransfer.types.includes(DRAG_TYPE) ? 'move' : 'copy';
    dropOn = folder;
  }
  async function dropTo(e, dest) {
    e.preventDefault();
    e.stopPropagation();
    dropOn = '';
    dragOver = false;
    const internal = e.dataTransfer.getData(DRAG_TYPE);
    if (internal) return moveMany(JSON.parse(internal), dest);
    if (!path && dest === path) return;
    const items = [...(e.dataTransfer.items || [])].map((it) => it.webkitGetAsEntry?.()).filter(Boolean);
    if (items.length) return queueEntries(items, dest);
    queueFiles([...e.dataTransfer.files].map((f) => ({ file: f, rel: f.name })), dest, []);
  }

  // Reads dropped folders recursively (keeps their structure).
  async function walk(entry, prefix, files, dirs) {
    if (entry.isFile) {
      const file = await new Promise((res, rej) => entry.file(res, rej)).catch(() => null);
      if (file) files.push({ file, rel: prefix + entry.name });
      return;
    }
    if (!entry.isDirectory) return;
    const dir = prefix + entry.name;
    dirs.push(dir);
    const reader = entry.createReader();
    for (;;) {
      const batch = await new Promise((res) => reader.readEntries(res, () => res([])));
      if (!batch.length) break;
      for (const child of batch) await walk(child, dir + '/', files, dirs);
    }
  }
  async function queueEntries(items, dest) {
    const files = [], dirs = [];
    for (const it of items) await walk(it, '', files, dirs);
    queueFiles(files, dest, dirs);
  }
  function pickInput(list, folder) {
    const files = [...list].map((f) => ({ file: f, rel: folder && f.webkitRelativePath ? f.webkitRelativePath : f.name }));
    const dirs = new Set();
    for (const f of files) {
      const parts = f.rel.split('/').slice(0, -1);
      for (let k = 1; k <= parts.length; k++) dirs.add(parts.slice(0, k).join('/'));
    }
    queueFiles(files, path, [...dirs]);
  }

  async function queueFiles(files, dest, dirs) {
    if (!files.length && !dirs.length) return;
    // Folders first, top-down; an existing folder is merged into.
    for (const d of [...dirs].sort((a, b) => a.split('/').length - b.split('/').length)) {
      const res = await api('/api/files/mkdir', { method: 'POST', body: { path: join(dest, d), op: 'exist_ok' } });
      if (lost(res)) return;
      if (!res.ok) { toast(`${d}: ${res.error}`, 'error', 8000); return; }
    }
    const here = dest === path ? new Set((entries || []).map((e) => e.name)) : new Set();
    const jobs = files.map((f) => ({ id: rid(), name: baseOf(f.rel), rel: f.rel, file: f.file, dest, done: 0, total: f.file.size, status: 'queued', error: '', overwrite: false, ctrl: null }));
    const clash = jobs.filter((j) => !j.rel.includes('/') && here.has(j.name));
    if (clash.length) {
      replaceJobs = jobs;
      replaceOpen = true;
      return;
    }
    startJobs(jobs);
  }
  function startJobs(jobs, overwrite = false) {
    replaceOpen = false;
    for (const j of jobs) j.overwrite = overwrite;
    uploads = [...uploads, ...jobs];
    panelOpen = true;
    pump();
  }
  const rid = () => [...crypto.getRandomValues(new Uint8Array(8))].map((b) => b.toString(16).padStart(2, '0')).join('');

  function pump() {
    const running = uploads.filter((u) => u.status === 'uploading').length;
    for (const u of uploads.filter((x) => x.status === 'queued').slice(0, Math.max(0, PARALLEL - running))) upload(u);
  }

  async function upload(u) {
    const item = uploads.find((x) => x.id === u.id);
    if (!item) return;
    item.status = 'uploading';
    item.ctrl = new AbortController();
    const target = join(item.dest, item.rel);
    const id = rid();
    const abort = () => fetch(`/api/files/upload?path=${encodeURIComponent(target)}&id=${id}&offset=0&abort=1`, { method: 'POST', credentials: 'same-origin' }).catch(() => {});
    try {
      for (let off = 0; off < item.total || off === 0; off += CHUNK) {
        const last = off + CHUNK >= item.total;
        const q = `path=${encodeURIComponent(target)}&id=${id}&offset=${off}${last ? '&final=1' : ''}${item.overwrite ? '&overwrite=1' : ''}`;
        const res = await fetch('/api/files/upload?' + q, { method: 'POST', credentials: 'same-origin', body: item.file.slice(off, off + CHUNK), signal: item.ctrl.signal });
        if (!res.ok) {
          if (res.status === 401) return onSessionLost();
          const body = await res.json().catch(() => ({}));
          throw new Error(body.error || 'The upload failed.');
        }
        item.done = Math.min(item.total, off + CHUNK);
        if (last) break;
      }
      item.status = 'done';
      if (item.dest === path) reloadSoon();
    } catch (err) {
      abort();
      if (item.status === 'cancelled') return pump();
      item.status = 'failed';
      item.error = err.name === 'AbortError' ? 'Cancelled.' : err.message === 'Failed to fetch' ? 'The connection to Box was lost.' : err.message;
    }
    item.ctrl = null;
    pump();
  }
  let reloadTimer;
  function reloadSoon() { clearTimeout(reloadTimer); reloadTimer = setTimeout(reload, 400); }
  function cancel(u) { u.status = 'cancelled'; u.ctrl?.abort(); if (!u.ctrl) pump(); }
  function retry(u, overwrite = false) { u.status = 'queued'; u.done = 0; u.error = ''; u.overwrite = overwrite || u.overwrite; pump(); }
  function clearDone() { uploads = uploads.filter((u) => u.status === 'queued' || u.status === 'uploading' || u.status === 'failed'); }

  function sortBy(key) { sort = { key, dir: sort.key === key ? -sort.dir : 1 }; }
  const dateFmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' });

  onMount(async () => {
    await loadPlaces();
    const want = new URLSearchParams(location.search).get('path');
    if (want) open(want);
  });
</script>

<svelte:window onkeydown={keydown} />

<div class="page fill">
  <header class="page-head compact"><h1 class="t-large">Files</h1></header>
  <div class="explorer tile">
    <!-- Toolbar -->
    <div class="bar-top">
      <div class="nav-btns">
        <button class="icon-btn sm side-toggle" onclick={() => (sideOpen = !sideOpen)} aria-label="Show places"><PanelLeft size={17} /></button>
        <button class="icon-btn sm" onclick={goBack} disabled={!back.length} aria-label="Back"><ChevronLeft size={18} /></button>
        <button class="icon-btn sm" onclick={goFwd} disabled={!fwd.length} aria-label="Forward"><ChevronRight size={18} /></button>
        <button class="icon-btn sm" onclick={goUp} disabled={!path} aria-label="Up one folder"><ArrowUp size={17} /></button>
      </div>

      {#if editing}
        <form class="pathbar editing" onsubmit={submitPath}>
          <input bind:this={pathInput} class="path-input mono" bind:value={pathText} onblur={() => (editing = false)}
            onkeydown={(e) => e.key === 'Escape' && (editing = false)} aria-label="Folder path" spellcheck="false" autocapitalize="none" />
        </form>
      {:else}
        <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions, a11y_no_static_element_interactions, a11y_interactive_supports_focus -->
        <div class="pathbar" role="navigation" aria-label="Folder path" onclick={(e) => e.target === e.currentTarget && path && editPath()}>
          <button class="crumb" onclick={() => open('')}><Server size={14} />This server</button>
          {#each crumbs as c (c.path)}
            <ChevronRight size={13} class="sep" />
            <button class="crumb" class:current={c.path === path} onclick={() => open(c.path)}
              ondragover={(e) => overFolder(e, c.path)} ondragleave={() => (dropOn = '')} ondrop={(e) => dropTo(e, c.path)} class:over={dropOn === c.path}>{c.label}</button>
          {/each}
          {#if path}<button class="edit-hint" onclick={editPath} aria-label="Type a path"><Pencil size={13} /></button>{/if}
        </div>
      {/if}

      <div class="tools">
        {#if path}
          <label class="search">
            <Search size={14} />
            <input bind:value={query} placeholder="Search in folder" aria-label="Search in this folder" spellcheck="false" />
            {#if query}<button class="clear" onclick={() => (query = '')} aria-label="Clear search"><X size={13} /></button>{/if}
          </label>
          <div class="seg" role="group" aria-label="View">
            <button class:on={view === 'list'} onclick={() => (view = 'list')} aria-label="List view" aria-pressed={view === 'list'}><List size={16} /></button>
            <button class:on={view === 'grid'} onclick={() => (view = 'grid')} aria-label="Icon view" aria-pressed={view === 'grid'}><LayoutGrid size={16} /></button>
          </div>
          <button class="icon-btn sm" onclick={() => askName('mkdir')} aria-label="New folder" title="New folder"><FolderPlus size={17} /></button>
          <button class="btn btn-tinted btn-sm" onclick={() => fileInput.click()} title="Upload files (or drop them here)"><Upload size={15} /><span class="lbl">Upload</span></button>
        {/if}
      </div>
      <input type="file" multiple hidden bind:this={fileInput} onchange={(e) => { pickInput(e.currentTarget.files, false); e.currentTarget.value = ''; }} />
      <input type="file" multiple hidden webkitdirectory bind:this={folderInput} onchange={(e) => { pickInput(e.currentTarget.files, true); e.currentTarget.value = ''; }} />
    </div>

    <div class="body">
      <!-- Sidebar: places and the folder tree -->
      <aside class="side" class:open={sideOpen}>
        <p class="t-section side-title">Places</p>
        <ul class="places">
          <li>
            <button class="place" class:active={!path} onclick={() => open('')}><Server size={15} /><span class="truncate">This server</span></button>
          </li>
          {#each places as p (p.path)}
            <li>
              <button class="place" class:active={root === p.path && path === p.path} class:over={dropOn === p.path} onclick={() => open(p.path)}
                ondragover={(e) => overFolder(e, p.path)} ondragleave={() => (dropOn = '')} ondrop={(e) => dropTo(e, p.path)}>
                {#if p.drive}<HardDrive size={15} />{:else}<Folder size={15} />{/if}
                <span class="place-text">
                  <span class="truncate">{p.path}</span>
                  {#if p.drive && p.total}
                    <span class="bar"><span style:transform="scaleX({pct(p.used, p.total) / 100})" style:background="var(--accent)"></span></span>
                  {/if}
                </span>
              </button>
            </li>
          {/each}
        </ul>
        {#if root}
          <p class="t-section side-title">Folders</p>
          <ul class="tree" role="tree" aria-label="Folders">
            {#key root}
              <FileTree path={root} label={baseOf(root) || root} current={path} open onOpen={(p) => open(p)}
                onDrop={(e, p) => dropTo(e, p)} />
            {/key}
          </ul>
        {/if}
      </aside>
      <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions, a11y_no_static_element_interactions, a11y_interactive_supports_focus -->
      {#if sideOpen}<div class="side-scrim" onclick={() => (sideOpen = false)} aria-hidden="true"></div>{/if}

      <!-- Content -->
      <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions, a11y_no_static_element_interactions, a11y_interactive_supports_focus -->
      <div class="main" role="region" aria-label="Folder" class:drag={dragOver}
        ondragover={(e) => { if (path && acceptsDrop(e)) { e.preventDefault(); dragOver = true; } }}
        ondragleave={(e) => { if (!e.currentTarget.contains(e.relatedTarget)) dragOver = false; }}
        ondrop={(e) => path && dropTo(e, path)}
        oncontextmenu={(e) => context(e, null)}>
        {#if error}
          <div class="pad"><div class="notice"><CircleAlert size={18} /><span>{error}</span></div></div>
        {/if}

        {#if !path}
          <div class="overview">
            <h2 class="t-title">This server</h2>
            <div class="drives">
              {#each places as p (p.path)}
                {@const used = pct(p.used, p.total)}
                <button class="drive" ondblclick={() => open(p.path)} onclick={() => open(p.path)}>
                  <span class="drive-ico">{#if p.drive}<HardDrive size={26} />{:else}<Folder size={26} />{/if}</span>
                  <span class="drive-text">
                    <span class="t-head truncate">{p.path}</span>
                    {#if p.total}
                      <span class="bar"><span style:transform="scaleX({used / 100})" style:background={used >= 90 ? 'var(--error)' : used >= 75 ? 'var(--warn)' : 'var(--accent)'}></span></span>
                      <span class="t-caption num">{bytes(p.total - p.used)} free of {bytes(p.total)}</span>
                    {/if}
                  </span>
                </button>
              {:else}
                {#each Array(3) as _}<div class="drive"><span class="skeleton" style:width="100%" style:height="3rem"></span></div>{/each}
              {/each}
            </div>
            <p class="t-caption">Change which folders appear here in Settings. System folders are never shown.</p>
          </div>
        {:else if !entries}
          <div class="pad">{#each Array(8) as _}<div class="skeleton line"></div>{/each}</div>
        {:else if !shown.length}
          <div class="empty">
            {#if query}
              <Search size={30} /><p class="t-head">No items match "{query}"</p>
            {:else}
              <FolderUp size={34} /><p class="t-head">This folder is empty</p><p class="t-caption">Drop files or folders here, or use Upload.</p>
            {/if}
          </div>
        {:else if view === 'list'}
          <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions, a11y_no_static_element_interactions, a11y_interactive_supports_focus -->
          <div class="table-wrap" bind:this={listEl} role="grid" tabindex="0" aria-label="Folder contents" aria-multiselectable="true"
            onclick={(e) => e.target === e.currentTarget && selected.clear()}>
            <div class="thead" role="row">
              {#each [['name', 'Name'], ['size', 'Size'], ['kind', 'Kind'], ['mtime', 'Modified']] as [k, l] (k)}
                <button class="th {k}" role="columnheader" aria-sort={sort.key === k ? (sort.dir > 0 ? 'ascending' : 'descending') : 'none'} onclick={() => sortBy(k)}>
                  {l}{#if sort.key === k}{#if sort.dir > 0}<ArrowUp size={12} />{:else}<ArrowDown size={12} />{/if}{/if}
                </button>
              {/each}
            </div>
            {#each shown as e, i (e.name)}
              {@const k = kindOf(e)}
              {@const Icon = k.icon}
              <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions, a11y_no_static_element_interactions, a11y_interactive_supports_focus -->
              <div class="tr" role="row" data-i={i} aria-selected={selected.has(e.name)} class:sel={selected.has(e.name)}
                class:cut={clip?.mode === 'cut' && clip.paths.includes(join(path, e.name))} class:over={dropOn === join(path, e.name)}
                draggable="true" ondragstart={(ev) => dragStart(ev, e, i)}
                ondragover={(ev) => e.dir && overFolder(ev, join(path, e.name))} ondragleave={() => (dropOn = '')}
                ondrop={(ev) => e.dir && dropTo(ev, join(path, e.name))}
                onclick={(ev) => select(ev, e, i)} ondblclick={() => activate(e)} oncontextmenu={(ev) => { ev.stopPropagation(); context(ev, e, i); }}>
                <span class="td name"><span class="ficon" class:folder={e.dir}><Icon size={17} /></span><span class="truncate">{e.name}</span></span>
                <span class="td size num">{e.dir ? '-' : bytes(e.size)}</span>
                <span class="td kind truncate">{k.label}</span>
                <span class="td mtime num truncate">{dateFmt.format(e.mtime * 1000)}</span>
              </div>
            {/each}
          </div>
        {:else}
          <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions, a11y_no_static_element_interactions, a11y_interactive_supports_focus -->
          <div class="grid" bind:this={listEl} role="grid" tabindex="0" aria-label="Folder contents" aria-multiselectable="true"
            onclick={(e) => e.target === e.currentTarget && selected.clear()}>
            {#each shown as e, i (e.name)}
              {@const k = kindOf(e)}
              {@const Icon = k.icon}
              <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions, a11y_no_static_element_interactions, a11y_interactive_supports_focus -->
              <div class="cell" role="gridcell" data-i={i} aria-selected={selected.has(e.name)} class:sel={selected.has(e.name)}
                class:cut={clip?.mode === 'cut' && clip.paths.includes(join(path, e.name))} class:over={dropOn === join(path, e.name)}
                draggable="true" ondragstart={(ev) => dragStart(ev, e, i)}
                ondragover={(ev) => e.dir && overFolder(ev, join(path, e.name))} ondragleave={() => (dropOn = '')}
                ondrop={(ev) => e.dir && dropTo(ev, join(path, e.name))}
                onclick={(ev) => select(ev, e, i)} ondblclick={() => activate(e)} oncontextmenu={(ev) => { ev.stopPropagation(); context(ev, e, i); }}>
                <span class="thumb" class:folder={e.dir}>
                  {#if !e.dir && PREVIEW_IMAGE.test(e.name) && e.size < 15 * 1024 * 1024}
                    <img src={fileURL(join(path, e.name), true)} alt="" loading="lazy" decoding="async" draggable="false" />
                  {:else}
                    <Icon size={40} strokeWidth={1.4} />
                  {/if}
                </span>
                <span class="cell-name">{e.name}</span>
                <span class="t-caption">{e.dir ? 'Folder' : bytes(e.size)}</span>
              </div>
            {/each}
          </div>
        {/if}

        {#if dragOver && path}
          <div class="drop-hint"><Upload size={22} /><span>Drop to upload into {baseOf(path) || path}</span></div>
        {/if}
      </div>
    </div>

    <!-- Status bar -->
    <div class="status t-caption num">
      {#if path && entries}
        <span>{shown.length === 1 ? '1 item' : `${shown.length} items`}{query ? ` matching "${query}"` : ''}</span>
        {#if selEntries.length}<span>{selEntries.length} selected{selSize ? `, ${bytes(selSize)}` : ''}</span>{/if}
        {#if clip}<span class="clip">{clip.paths.length} {clip.mode === 'copy' ? 'copied' : 'cut'}: paste with {shortcut('V')}</span>{/if}
      {:else}
        <span>{places.length} places</span>
      {/if}
      {#if activeUploads}<button class="up-link" onclick={() => (panelOpen = true)}><Loader size={12} class="spinner" />Uploading {activeUploads}</button>{/if}
    </div>
  </div>
</div>

<!-- Upload panel -->
{#if uploads.length && panelOpen}
  <div class="uploads" role="region" aria-label="Uploads">
    <div class="up-head">
      <span class="t-head">{activeUploads ? `Uploading ${activeUploads} of ${uploads.length}` : 'Uploads finished'}</span>
      <button class="btn btn-gray btn-sm" onclick={clearDone} disabled={!uploads.some((u) => u.status === 'done' || u.status === 'cancelled')}>Clear</button>
      <button class="icon-btn sm" onclick={() => (panelOpen = false)} aria-label="Hide uploads"><X size={16} /></button>
    </div>
    <ul class="up-list">
      {#each uploads as u (u.id)}
        <li class="up">
          <span class="up-ico">
            {#if u.status === 'uploading'}<Loader size={15} class="spinner" />
            {:else if u.status === 'done'}<CircleCheck size={15} />
            {:else if u.status === 'failed'}<CircleAlert size={15} />
            {:else}<Upload size={15} />{/if}
          </span>
          <span class="up-main">
            <span class="truncate" title={u.rel}>{u.rel}</span>
            {#if u.status === 'failed'}
              <span class="t-caption err">{u.error}</span>
            {:else if u.status === 'cancelled'}
              <span class="t-caption">Cancelled</span>
            {:else}
              <span class="bar"><span style:transform="scaleX({u.total ? u.done / u.total : u.status === 'done' ? 1 : 0})" style:background="var(--accent)"></span></span>
            {/if}
          </span>
          <span class="t-caption num up-size">{u.status === 'done' ? bytes(u.total) : `${bytes(u.done)} / ${bytes(u.total)}`}</span>
          {#if u.status === 'queued' || u.status === 'uploading'}
            <button class="icon-btn sm" onclick={() => cancel(u)} aria-label="Cancel {u.name}"><X size={15} /></button>
          {:else if u.status === 'failed' || u.status === 'cancelled'}
            {#if /already exists/i.test(u.error)}<button class="btn btn-gray btn-sm" onclick={() => retry(u, true)}>Replace</button>{/if}
            <button class="icon-btn sm" onclick={() => retry(u)} aria-label="Retry {u.name}"><RotateCcw size={15} /></button>
          {/if}
        </li>
      {/each}
    </ul>
  </div>
{/if}

<ContextMenu bind:menu onPick={pick} />
<ShareLinkDialog bind:open={shareOpen} path={share.path} name={share.name} dir={share.dir} origin={share.origin} />

<Dialog bind:open={nameOpen} label={nameMode === 'mkdir' ? 'New folder' : 'Rename'} locked={nameBusy}>
  <form class="dlg" onsubmit={submitName}>
    <h2 class="t-title">{nameMode === 'mkdir' ? 'New folder' : `Rename ${nameFor?.name}`}</h2>
    {#if nameError}<div class="notice" role="alert">{nameError}</div>{/if}
    <input class="input" bind:value={nameValue} aria-label="Name" spellcheck="false" required
      onfocus={(e) => { const v = e.currentTarget.value, d = nameFor && !nameFor.dir ? v.lastIndexOf('.') : -1; e.currentTarget.setSelectionRange(0, d > 0 ? d : v.length); }} />
    <div class="buttons">
      <button type="button" class="btn btn-gray" data-close onclick={() => (nameOpen = false)} disabled={nameBusy}>Cancel</button>
      <button class="btn btn-primary" disabled={nameBusy || !nameValue.trim() || nameValue.includes('/')}>{#if nameBusy}<Loader size={16} class="spinner" />{/if}{nameMode === 'mkdir' ? 'Create' : 'Rename'}</button>
    </div>
  </form>
</Dialog>

<Dialog bind:open={moveOpen} label="Move to" locked={moveBusy}>
  <form class="dlg" onsubmit={submitMove}>
    <h2 class="t-title">Move {delList.length === 1 ? delList[0]?.name : `${delList.length} items`}</h2>
    <p class="t-sub">Type the folder to move {delList.length === 1 ? 'it' : 'them'} to. You can also drag items onto a folder.</p>
    {#if moveError}<div class="notice" role="alert">{moveError}</div>{/if}
    <input class="input mono" bind:value={moveTo} aria-label="Destination folder" spellcheck="false" autocapitalize="none" required />
    <div class="buttons">
      <button type="button" class="btn btn-gray" data-close onclick={() => (moveOpen = false)} disabled={moveBusy}>Cancel</button>
      <button class="btn btn-primary" disabled={moveBusy || !moveTo.trim()}>{#if moveBusy}<Loader size={16} class="spinner" />{/if}Move</button>
    </div>
  </form>
</Dialog>

<Dialog bind:open={delOpen} label="Delete" locked={delBusy}>
  <div class="dlg">
    <h2 class="t-title">Delete {delList.length === 1 ? delList[0]?.name : `${delList.length} items`}?</h2>
    <p class="t-sub">{delList.some((x) => x.dir) ? 'Folders are deleted with everything in them.' : delList.length === 1 ? 'The file is deleted.' : 'The files are deleted.'} This cannot be undone.</p>
    <div class="buttons">
      <button class="btn btn-gray" data-close onclick={() => (delOpen = false)} disabled={delBusy}>Cancel</button>
      <button class="btn btn-danger" onclick={doDelete} disabled={delBusy}>{#if delBusy}<Loader size={16} class="spinner" />{/if}Delete</button>
    </div>
  </div>
</Dialog>

<Dialog bind:open={replaceOpen} label="Replace files">
  <div class="dlg">
    <h2 class="t-title">Replace existing files?</h2>
    <p class="t-sub">Some files with the same name are already in this folder.</p>
    <div class="buttons">
      <button class="btn btn-gray" data-close onclick={() => (replaceOpen = false)}>Cancel</button>
      <button class="btn btn-gray" onclick={() => startJobs(replaceJobs.filter((j) => j.rel.includes('/') || !(entries || []).some((e) => e.name === j.name)))}>Skip them</button>
      <button class="btn btn-primary" onclick={() => startJobs(replaceJobs, true)}>Replace</button>
    </div>
  </div>
</Dialog>

<Dialog bind:open={infoOpen} label="Info">
  <div class="dlg">
    {#if infoFor}
      {@const Icon = kindOf(infoFor).icon}
      <div class="info-head">
        <span class="thumb sm" class:folder={infoFor.dir}><Icon size={30} strokeWidth={1.5} /></span>
        <div><h2 class="t-title truncate">{infoFor.name}</h2><p class="t-caption">{infoFor.kind}</p></div>
      </div>
      <div class="group inset">
        <div class="row"><span class="row-label">Where</span><span class="row-value mono truncate" title={infoFor.path}>{parentOf(infoFor.path)}</span></div>
        <div class="row"><span class="row-label">Size</span><span class="row-value num">
          {#if !infoFor.dir}{bytes(infoFor.size)} ({infoFor.size.toLocaleString()} bytes)
          {:else if !infoSize}<Loader size={14} class="spinner" />
          {:else if infoSize.error}{infoSize.error}
          {:else}{bytes(infoSize.size)}{infoSize.partial ? ' or more' : ''}, {infoSize.files.toLocaleString()} files, {infoSize.dirs.toLocaleString()} folders{/if}
        </span></div>
        <div class="row"><span class="row-label">Modified</span><span class="row-value">{dateFmt.format(infoFor.mtime * 1000)}</span></div>
        <div class="row"><span class="row-label">Permissions</span><span class="row-value mono">{infoFor.mode}</span></div>
        {#if infoFor.link}<div class="row"><span class="row-label">Link</span><span class="row-value">Yes, it points somewhere else</span></div>{/if}
      </div>
    {/if}
    <div class="buttons"><button class="btn btn-gray" data-close onclick={() => (infoOpen = false)}>Close</button></div>
  </div>
</Dialog>

<Dialog bind:open={viewOpen} label="Preview" wide>
  <div class="dlg">
    <div class="dlg-head">
      <h2 class="t-title truncate">{viewFor?.name}</h2>
      <a class="btn btn-gray btn-sm" href={viewFor ? fileURL(viewFor.path, false) : '#'}><Download size={15} />Download</a>
    </div>
    {#if viewFor && PREVIEW_IMAGE.test(viewFor.name)}
      <img class="preview" src={fileURL(viewFor.path, true)} alt={viewFor.name} />
    {:else}
      <pre class="mono text">{viewText || 'Loading'}</pre>
    {/if}
    <div class="buttons"><button class="btn btn-gray" data-close onclick={() => (viewOpen = false)}>Close</button></div>
  </div>
</Dialog>

<style>
  .explorer { flex: 1; min-height: 0; display: flex; flex-direction: column; padding: 0; overflow: hidden; }

  /* Toolbar */
  .bar-top { display: flex; align-items: center; gap: 0.5rem; padding: 0.5rem 0.625rem; border-bottom: 1px solid var(--hairline); }
  .nav-btns, .tools { display: flex; align-items: center; gap: 0.125rem; }
  .tools { gap: 0.375rem; margin-left: auto; }
  .icon-btn.sm { width: 2.25rem; height: 2.25rem; border-radius: 0.625rem; }
  .icon-btn:disabled { opacity: 0.35; cursor: default; background: none; }
  .side-toggle { display: none; }
  .pathbar {
    flex: 1; min-width: 0; display: flex; align-items: center; gap: 0.125rem; height: 2.25rem; padding: 0 0.25rem;
    border-radius: 0.625rem; background: var(--fill); overflow-x: auto; scrollbar-width: none; cursor: text;
  }
  .pathbar :global(.sep) { color: var(--text-3); flex-shrink: 0; }
  .crumb {
    display: inline-flex; align-items: center; gap: 0.375rem; flex-shrink: 0; height: 1.75rem; padding: 0 0.5rem;
    border: 0; border-radius: 0.4375rem; background: none; color: var(--text-2); font: inherit; font-size: 0.8125rem; cursor: pointer; white-space: nowrap;
  }
  .crumb:hover { background: var(--fill-2); color: var(--text); }
  .crumb.current { color: var(--text); font-weight: 600; }
  .crumb.over { background: var(--accent-fill); color: var(--accent); }
  .edit-hint { margin-left: auto; display: grid; place-items: center; width: 1.75rem; height: 1.75rem; border: 0; border-radius: 0.4375rem; background: none; color: var(--text-3); cursor: pointer; flex-shrink: 0; }
  .edit-hint:hover { color: var(--text); background: var(--fill-2); }
  .pathbar.editing { padding: 0; cursor: auto; }
  .path-input { width: 100%; height: 100%; padding: 0 0.75rem; border: 0; border-radius: 0.625rem; background: var(--surface); box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 45%, transparent); }
  .search { display: flex; align-items: center; gap: 0.375rem; height: 2.25rem; padding: 0 0.625rem; border-radius: 0.625rem; background: var(--fill); color: var(--text-3); width: 12rem; }
  .search input { flex: 1; min-width: 0; border: 0; background: none; font-size: 0.8125rem; color: var(--text); }
  .search input::placeholder { color: var(--text-3); }
  .search input:focus-visible { outline: none; }
  .search:focus-within { box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 45%, transparent); }
  .clear { display: grid; place-items: center; width: 1.25rem; height: 1.25rem; border: 0; border-radius: 50%; background: var(--fill-2); color: var(--text-2); cursor: pointer; padding: 0; }
  .seg { display: flex; padding: 0.1875rem; border-radius: 0.625rem; background: var(--fill); }
  .seg button { display: grid; place-items: center; width: 2rem; height: 1.875rem; border: 0; border-radius: 0.4375rem; background: none; color: var(--text-2); cursor: pointer; transition: background-color 150ms ease; }
  .seg button.on { background: var(--surface); color: var(--text); box-shadow: 0 1px 2px rgba(0, 0, 0, 0.12); }

  /* Body */
  .body { flex: 1; min-height: 0; display: flex; position: relative; }
  .side { width: 14.5rem; flex-shrink: 0; overflow-y: auto; padding: 0.75rem 0.5rem; border-right: 1px solid var(--hairline); background: color-mix(in srgb, var(--surface-2) 60%, transparent); }
  :global(.dark) .side { background: color-mix(in srgb, var(--bg) 35%, transparent); }
  .side-title { margin: 0.5rem 0.625rem 0.375rem; }
  .side-title:first-child { margin-top: 0; }
  .places, .tree { list-style: none; margin: 0 0 0.5rem; padding: 0; }
  .place {
    display: flex; align-items: center; gap: 0.5rem; width: 100%; min-height: 2rem; padding: 0.25rem 0.625rem;
    border: 0; border-radius: 0.5rem; background: none; color: var(--text); font: inherit; font-size: 0.8125rem; text-align: left; cursor: pointer;
  }
  .place :global(svg) { color: var(--accent); }
  .place:hover { background: var(--fill); }
  .place.active { background: var(--fill-2); font-weight: 600; }
  .place.over { background: var(--accent-fill); box-shadow: inset 0 0 0 1.5px var(--accent); }
  .place-text { flex: 1; min-width: 0; display: grid; gap: 0.25rem; }
  .place .bar { height: 0.1875rem; }
  .side-scrim { display: none; }

  .main { flex: 1; min-width: 0; overflow: auto; position: relative; outline: none; }
  .main.drag { box-shadow: inset 0 0 0 2px var(--accent); background: color-mix(in srgb, var(--accent) 5%, transparent); }
  .pad { padding: 1rem; display: grid; gap: 0.5rem; }
  .skeleton.line { height: 1.5rem; }
  .empty { height: 100%; min-height: 14rem; display: grid; place-content: center; justify-items: center; gap: 0.375rem; color: var(--text-3); text-align: center; }
  .empty p { margin: 0; }
  .empty .t-head { color: var(--text-2); }

  /* List view */
  .table-wrap { min-height: 100%; padding-bottom: 1rem; }
  .thead, .tr { display: grid; grid-template-columns: minmax(10rem, 1fr) 6.5rem 9rem 11rem; align-items: center; column-gap: 0.75rem; padding: 0 1rem; }
  .thead { position: sticky; top: 0; z-index: 2; height: 2.125rem; background: color-mix(in srgb, var(--surface) 88%, transparent); -webkit-backdrop-filter: blur(20px); backdrop-filter: blur(20px); border-bottom: 1px solid var(--hairline); }
  .th { display: inline-flex; align-items: center; gap: 0.25rem; height: 100%; padding: 0; border: 0; background: none; color: var(--text-2); font: inherit; font-size: 0.75rem; font-weight: 600; cursor: pointer; text-align: left; }
  .th.size { justify-content: flex-end; }
  .th:hover { color: var(--text); }
  .tr { min-height: 2.25rem; margin: 0 0.375rem; padding: 0 0.625rem; border-radius: 0.5rem; cursor: default; user-select: none; }
  .tr:nth-child(odd) { background: color-mix(in srgb, var(--fill) 45%, transparent); }
  .tr:hover { background: var(--fill); }
  .tr.sel, .cell.sel { background: var(--accent); color: var(--on-accent); }
  .tr.sel .td, .tr.sel .ficon { color: var(--on-accent); }
  .tr.cut, .cell.cut { opacity: 0.5; }
  .tr.over, .cell.over { box-shadow: inset 0 0 0 2px var(--accent); background: var(--accent-fill); color: var(--text); }
  .td { font-size: 0.8125rem; min-width: 0; }
  .td.name { display: flex; align-items: center; gap: 0.625rem; font-weight: 500; }
  .td.size { text-align: right; color: var(--text-2); }
  .td.kind, .td.mtime { color: var(--text-2); }
  .ficon { display: grid; place-items: center; color: var(--text-2); }
  .ficon.folder { color: var(--accent); }

  /* Icon view */
  .grid { min-height: 100%; display: grid; grid-template-columns: repeat(auto-fill, minmax(7.25rem, 1fr)); gap: 0.375rem; padding: 0.875rem; align-content: start; }
  .cell { display: grid; justify-items: center; align-content: start; gap: 0.25rem; padding: 0.625rem 0.375rem; border-radius: 0.75rem; text-align: center; cursor: default; user-select: none; }
  .cell:hover { background: var(--fill); }
  .cell.sel .t-caption { color: inherit; opacity: 0.85; }
  .thumb { display: grid; place-items: center; width: 4.5rem; height: 4.5rem; color: var(--text-2); overflow: hidden; }
  .thumb.folder { color: var(--accent); }
  .thumb img { width: 100%; height: 100%; object-fit: contain; }
  .cell.sel .thumb { color: inherit; }
  .cell-name { font-size: 0.8125rem; font-weight: 500; line-height: 1.25; max-width: 100%; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; word-break: break-word; }

  .drop-hint {
    position: absolute; left: 50%; bottom: 1rem; transform: translateX(-50%); display: flex; align-items: center; gap: 0.5rem;
    padding: 0.625rem 1rem; border-radius: 999px; background: var(--accent); color: var(--on-accent); font-weight: 600; pointer-events: none;
    box-shadow: var(--shadow-float);
  }

  /* Overview */
  .overview { padding: 1.25rem; display: grid; gap: 1rem; align-content: start; }
  .overview h2 { margin: 0; }
  .drives { display: grid; grid-template-columns: repeat(auto-fill, minmax(15rem, 1fr)); gap: 0.75rem; }
  .drive { display: flex; align-items: center; gap: 0.875rem; padding: 0.875rem 1rem; border: 0; border-radius: 1rem; background: var(--fill); color: inherit; font: inherit; text-align: left; cursor: pointer; transition: transform 100ms ease-out, background-color 150ms ease; }
  .drive:hover { background: var(--fill-2); }
  .drive:active { transform: scale(0.98); }
  .drive-ico { display: grid; place-items: center; width: 3rem; height: 3rem; color: var(--accent); flex-shrink: 0; }
  .drive-text { flex: 1; min-width: 0; display: grid; gap: 0.375rem; }

  /* Status bar */
  .status { display: flex; align-items: center; gap: 1rem; min-height: 2rem; padding: 0 1rem; border-top: 1px solid var(--hairline); color: var(--text-2); }
  .status .clip { color: var(--accent); }
  .up-link { margin-left: auto; display: inline-flex; align-items: center; gap: 0.375rem; border: 0; background: none; color: var(--accent); font: inherit; cursor: pointer; }

  /* Upload panel */
  .uploads {
    position: fixed; right: 1rem; bottom: 1rem; z-index: 35; width: min(24rem, calc(100vw - 2rem)); max-height: 22rem;
    display: flex; flex-direction: column; border-radius: 1.125rem; background: var(--material); box-shadow: var(--shadow-float);
    -webkit-backdrop-filter: blur(30px) saturate(180%); backdrop-filter: blur(30px) saturate(180%);
    animation: rise var(--spring-t) var(--spring);
  }
  @keyframes rise { from { transform: translateY(1rem); opacity: 0; } }
  .up-head { display: flex; align-items: center; gap: 0.5rem; padding: 0.625rem 0.5rem 0.5rem 1rem; }
  .up-head .t-head { flex: 1; }
  .up-list { list-style: none; margin: 0; padding: 0 0.375rem 0.5rem; overflow-y: auto; }
  .up { display: flex; align-items: center; gap: 0.625rem; padding: 0.375rem 0.5rem; border-radius: 0.625rem; }
  .up:hover { background: var(--fill); }
  .up-ico { display: grid; place-items: center; color: var(--text-2); }
  .up-main { flex: 1; min-width: 0; display: grid; gap: 0.3125rem; font-size: 0.8125rem; }
  .up-main .bar { height: 0.25rem; }
  .up-size { flex-shrink: 0; }
  .err { color: var(--error); }

  .info-head { display: flex; align-items: center; gap: 0.875rem; }
  .info-head > div { min-width: 0; }
  .info-head h2, .info-head p { margin: 0; }
  .thumb.sm { width: 3.25rem; height: 3.25rem; }
  .inset { box-shadow: none; background: var(--fill); }
  .preview { max-width: 100%; max-height: 65svh; justify-self: center; border-radius: var(--r-control); }
  .text { margin: 0; max-height: 60svh; overflow: auto; padding: 0.875rem 1rem; border-radius: var(--r-control); background: var(--term-bg); font-size: 0.75rem; line-height: 1.5; white-space: pre-wrap; word-break: break-word; }

  @media (max-width: 1099px) {
    .thead, .tr { grid-template-columns: minmax(8rem, 1fr) 5.5rem 9.5rem; }
    .th.kind, .td.kind { display: none; }
  }
  @media (max-width: 899px) {
    .side-toggle { display: inline-grid; }
    .side { position: absolute; z-index: 5; top: 0; bottom: 0; left: 0; transform: translateX(-105%); visibility: hidden; transition: transform var(--spring-t) var(--spring), visibility 0s linear var(--spring-t); background: var(--surface); box-shadow: var(--shadow-float); }
    .side.open { transform: none; visibility: visible; transition: transform var(--spring-t) var(--spring); }
    .side-scrim { display: block; position: absolute; inset: 0; z-index: 4; background: var(--scrim); }
    .search { width: 8rem; }
  }
  @media (max-width: 639px) {
    .bar-top { flex-wrap: wrap; }
    .pathbar { order: 3; flex-basis: 100%; }
    .search { flex: 1; width: auto; min-width: 0; }
    .search input { width: 100%; }
    .tools { flex: 1; }
    .lbl { display: none; }
    .thead, .tr { grid-template-columns: minmax(0, 1fr) 5rem; }
    .th.mtime, .td.mtime { display: none; }
    .grid { grid-template-columns: repeat(auto-fill, minmax(6rem, 1fr)); }
  }
</style>
