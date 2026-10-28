// In-app navigation for code outside the page tree (toast actions). App.svelte sets go.
export const router = { go: (to) => location.assign(to) };
