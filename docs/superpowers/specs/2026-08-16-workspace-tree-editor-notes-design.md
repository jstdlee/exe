# Workspace Tree, Editor, and Notes Design

## Goal

Finish the mobile-responsive desktop layout by removing redundant side-list collapse behavior, making Workspace the single file navigator, and keeping editors focused on editing one selected file.

## Approved behavior

- Notes surfaces do not show a hide/collapse notes/header control.
- Workspace Finder does not show a hide-files control.
- Editor windows do not show a hide-info control.
- The desktop no longer exposes the standalone Editor app as a window/icon.
- Workspace is the navigation surface for files and folders.
- Workspace displays a tree view and supports navigating between files.
- Right-clicking a Workspace node supports New Text File, New Folder, and Upload at the node location. If the node is a file, actions target the file parent folder.
- Workspace and editor file choices hide dotfiles and `.Trash` by default.
- The existing internal editor remains the single-file editor and autosaves content.
- Existing Workspace file operations remain token-guarded and confined to `~/.exe/workspace`.

## Architecture

The daemon already exposes the needed Workspace file APIs: recursive file listing, single-directory listing with folder entries, file PUT/DELETE, mkdir, upload, and move-to-trash. The implementation stays in the existing UI shell rather than adding new backend storage.

The Workspace window changes from icon grid to tree view. Each tree node maps to a Workspace relative path. Folder nodes lazy-load children from `/v1/workspace?dir=...`; file nodes open the existing internal single-file editor or image viewer. Context menus compute a target directory from the clicked node.

The standalone `data/apps/Editor` bundle is kept on disk but filtered out of the desktop app list so it no longer appears as a separate app window. Its page is simplified to single-file mode for direct URL use.
