# Core protocol source

`wayland.xml` is from Wayland 1.26.0, upstream GitLab
`https://gitlab.freedesktop.org/wayland/wayland`, tag `1.26.0`, peeled commit
`87cc8a8728a923fc57938faa81ba0e74f34ecdc7`.
SHA-256: `cc860987e54f8d85c940e97fa1270c69b6e4ad31fbcf5a7f00107ce1157f5e07`.
License: MIT (full copyright and grant in `wayland.xml` `<copyright>`).

## Required extensions

The following XML files come from `https://gitlab.freedesktop.org/wayland/wayland-protocols`,
tag `1.49` (peeled commit `ee78491a237eaff9389a0ccf8680521d074407d3`).
Each file includes its upstream `<copyright>` and MIT-style license grant.
SHA-256 (of the vendored XML bytes):

| Upstream path (under tag 1.49) | Vendored path | SHA-256 |
|---|---|---|
| `stable/xdg-shell/xdg-shell.xml` | `xdgshell/xdg-shell.xml` | `7ba7f9c8473deee674cb1f154a18abd0bb0cc072604fc055b0c15e459fc4c7df` |
| `stable/linux-dmabuf/linux-dmabuf-v1.xml` | `linuxdmabuf/linux-dmabuf-v1.xml` | `2735bb4589cbb364dfcb6a821dd3abbf0c0257956b716b9691718ddfd453df4c` |
| `staging/linux-drm-syncobj/linux-drm-syncobj-v1.xml` | `drmsyncobj/linux-drm-syncobj-v1.xml` | `5ede41a56eced6aa635caa7ab2996066bff9fd211620dbf9f73aafab740bec71` |
| `stable/viewporter/viewporter.xml` | `viewporter/viewporter.xml` | `dcb12279a03746301fe490aaed4b38a403485a925abfce2ccfceb644e104fe71` |
| `staging/fractional-scale/fractional-scale-v1.xml` | `fractionalscale/fractional-scale-v1.xml` | `5941de5d28f427ecdadddc8623a6f6af0a30b0ab4726847236ba7a7652b81316` |
| `unstable/text-input/text-input-unstable-v3.xml` | `textinput/text-input-unstable-v3.xml` | `160815cda13c285a30df1971d8f0a5b3aac07071537df59916d2f748b18e4f59` |
