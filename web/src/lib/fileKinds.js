// File kind (icon, label) from a file name, Finder style.
import Folder from '@lucide/svelte/icons/folder';
import FileIcon from '@lucide/svelte/icons/file';
import FileText from '@lucide/svelte/icons/file-text';
import FileImage from '@lucide/svelte/icons/file-image';
import FileVideo from '@lucide/svelte/icons/file-video-camera';
import FileMusic from '@lucide/svelte/icons/file-music';
import FileArchive from '@lucide/svelte/icons/file-archive';
import FileCode from '@lucide/svelte/icons/file-code';
import FileType from '@lucide/svelte/icons/file-type';
import FileSymlink from '@lucide/svelte/icons/file-symlink';

const KINDS = [
  ['image', 'Image', FileImage, /\.(png|jpe?g|gif|webp|bmp|svg|heic|avif|tiff?|ico)$/i],
  ['video', 'Video', FileVideo, /\.(mp4|mkv|mov|avi|webm|m4v|wmv|flv|ts)$/i],
  ['audio', 'Audio', FileMusic, /\.(mp3|flac|wav|ogg|m4a|aac|opus|wma)$/i],
  ['archive', 'Archive', FileArchive, /\.(zip|tar|gz|tgz|bz2|xz|zst|7z|rar|iso|deb|rpm)$/i],
  ['pdf', 'PDF document', FileType, /\.pdf$/i],
  ['code', 'Code', FileCode, /\.(js|mjs|ts|jsx|tsx|svelte|vue|go|py|rb|php|java|kt|c|h|cpp|hpp|rs|sh|bash|zsh|ps1|html?|css|scss|sql|lua|pl|swift|dockerfile)$/i],
  ['text', 'Text', FileText, /\.(txt|log|md|json|ya?ml|conf|cfg|env|ini|toml|csv|xml|properties|service|rules)$/i],
];

// Shown inline by the server (see previewTypes in files_web.go).
export const PREVIEW_IMAGE = /\.(png|jpe?g|gif|webp)$/i;
export const PREVIEW_TEXT = /\.(txt|log|md|json|ya?ml|conf|env|sh|ini|toml|csv)$/i;

export function kindOf(e) {
  if (e.dir) return { key: 'folder', label: e.link ? 'Folder (link)' : 'Folder', icon: Folder };
  for (const [key, label, icon, re] of KINDS) if (re.test(e.name)) return { key, label, icon };
  if (e.link) return { key: 'link', label: 'Link', icon: FileSymlink };
  const ext = e.name.includes('.') ? e.name.split('.').pop().toUpperCase() : '';
  return { key: 'file', label: ext && ext.length <= 5 ? `${ext} file` : 'File', icon: FileIcon };
}
