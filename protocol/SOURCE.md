# Core protocol source

`wayland.xml` is from Wayland 1.26.0, upstream GitLab
`https://gitlab.freedesktop.org/wayland/wayland`, tag `1.26.0`, peeled commit
`87cc8a8728a923fc57938faa81ba0e74f34ecdc7`.
SHA-256: `cc860987e54f8d85c940e97fa1270c69b6e4ad31fbcf5a7f00107ce1157f5e07`.
License: MIT (full copyright and grant in `wayland.xml` `<copyright>`).

## wayland-protocols extensions

The following XML files come from `https://gitlab.freedesktop.org/wayland/wayland-protocols`,
tag `1.49` (peeled commit `ee78491a237eaff9389a0ccf8680521d074407d3`).
Each XML retains its upstream `<copyright>` and license grant; consult the file for its exact terms.
SHA-256 (of the vendored XML bytes):

| Upstream path (under tag 1.49) | Vendored path | SHA-256 |
|---|---|---|
| `stable/xdg-shell/xdg-shell.xml` | `xdgshell/xdg-shell.xml` | `7ba7f9c8473deee674cb1f154a18abd0bb0cc072604fc055b0c15e459fc4c7df` |
| `stable/linux-dmabuf/linux-dmabuf-v1.xml` | `linuxdmabuf/linux-dmabuf-v1.xml` | `2735bb4589cbb364dfcb6a821dd3abbf0c0257956b716b9691718ddfd453df4c` |
| `staging/linux-drm-syncobj/linux-drm-syncobj-v1.xml` | `drmsyncobj/linux-drm-syncobj-v1.xml` | `5ede41a56eced6aa635caa7ab2996066bff9fd211620dbf9f73aafab740bec71` |
| `stable/viewporter/viewporter.xml` | `viewporter/viewporter.xml` | `dcb12279a03746301fe490aaed4b38a403485a925abfce2ccfceb644e104fe71` |
| `staging/fractional-scale/fractional-scale-v1.xml` | `fractionalscale/fractional-scale-v1.xml` | `5941de5d28f427ecdadddc8623a6f6af0a30b0ab4726847236ba7a7652b81316` |
| `unstable/text-input/text-input-unstable-v3.xml` | `textinput/text-input-unstable-v3.xml` | `160815cda13c285a30df1971d8f0a5b3aac07071537df59916d2f748b18e4f59` |
| `staging/cursor-shape/cursor-shape-v1.xml` | `cursorshape/cursor-shape-v1.xml` | `bb57d91e53a79dadab7c612dab87c233393cee73673feefa7442cfbfdd9aed2f` |
| `stable/tablet/tablet-v2.xml` | `tablet/tablet-v2.xml` | `ac1128b26c779cf90b9ed71182ba5e34a7262826789adae206d825a4d45908b4` |
| `staging/alpha-modifier/alpha-modifier-v1.xml` | `alphamodifier/alpha-modifier-v1.xml` | `f0b4a43b9e48783fafdb46319fa72b34a41afd932f8613d7cadaf7203f2ffa35` |
| `staging/color-management/color-management-v1.xml` | `colormanagement/color-management-v1.xml` | `18a2678e3352c3be3fbeed20005d48852b7235d0d78f08b9c987a0b8e91449f9` |
| `staging/color-representation/color-representation-v1.xml` | `colorrepresentation/color-representation-v1.xml` | `b824688e0e5c1d01e12afe1d3f82503092ec7599affcd6471fda6d296d4f7e3b` |
| `staging/commit-timing/commit-timing-v1.xml` | `committiming/commit-timing-v1.xml` | `ff3fcf31d13d44bad756a50fda663a43008bacbbd2bf8cba3a1bf5ffe7f54172` |
| `staging/content-type/content-type-v1.xml` | `contenttype/content-type-v1.xml` | `203d17a26baa2ab4a8c2c9cb737c953fa3263c73a0a8aa6e552a41b3eac2196d` |
| `staging/ext-data-control/ext-data-control-v1.xml` | `datacontrol/ext-data-control-v1.xml` | `293deba5dfdb974fbcf8dcf891a30a51a36a8bba0047dc6bdf6a03194089b7fb` |
| `staging/drm-lease/drm-lease-v1.xml` | `drmlease/drm-lease-v1.xml` | `18f325e92285473eca0567ea1038abb16849b8638b36e458ff551f3ca68ef80c` |
| `staging/ext-foreign-toplevel-list/ext-foreign-toplevel-list-v1.xml` | `extforeigntoplevel/ext-foreign-toplevel-list-v1.xml` | `6b2ffb3a07d679d1c4170263adfefb9a7c37dd34ed3db5180bf58cb8ff8fbd0b` |
| `staging/fifo/fifo-v1.xml` | `fifo/fifo-v1.xml` | `0b6c31dfe6bca19b712745c9de7a4a398558cfa1087b1938e96fd3b10ffc1069` |
| `unstable/idle-inhibit/idle-inhibit-unstable-v1.xml` | `idleinhibit/idle-inhibit-unstable-v1.xml` | `c2ac9f002c6669ca6c02eb9b587b8e9108e3ce6deab4a2de58b576f4f864ca38` |
| `staging/ext-idle-notify/ext-idle-notify-v1.xml` | `idlenotify/ext-idle-notify-v1.xml` | `e56a9c22684e6b46655f7221b798328e304c1efc8f63f01241a1cf8c070f4c30` |
| `staging/ext-image-capture-source/ext-image-capture-source-v1.xml` | `imagecapturesource/ext-image-capture-source-v1.xml` | `4ef41d15e4cdb9f550158391358ceec724a6708b43b7efedec03121a6bb8458d` |
| `staging/ext-image-copy-capture/ext-image-copy-capture-v1.xml` | `imagecopycapture/ext-image-copy-capture-v1.xml` | `41a446653f788fabb404cab3168c0bd667c1ff4b54f1f0bb1e18810a1f47d73f` |
| `unstable/pointer-constraints/pointer-constraints-unstable-v1.xml` | `pointerconstraints/pointer-constraints-unstable-v1.xml` | `f980fac900ba1dcfbbe97f588fc17b893926bd2b57624563653a1bfe4d035948` |
| `staging/pointer-warp/pointer-warp-v1.xml` | `pointerwarp/pointer-warp-v1.xml` | `1389e89c68c6f6d231fd2dcda57fa9c75c5230d0ac1deffa8d80a7648e197e66` |
| `stable/presentation-time/presentation-time.xml` | `presentation/presentation-time.xml` | `dffac93bcb2bb1d8c385e72b8a8c2c0d4d79a336866322f3ba886dce2b27b1e2` |
| `unstable/primary-selection/primary-selection-unstable-v1.xml` | `primaryselection/primary-selection-unstable-v1.xml` | `d568482ba84df6e531698b1f531810860995ca24d69495427fe43aee6017f52c` |
| `unstable/relative-pointer/relative-pointer-unstable-v1.xml` | `relativepointer/relative-pointer-unstable-v1.xml` | `ab4930dd3084f732b6fdd12ee6dbd0a112a758ed8a57b170366391dbeb1a22ff` |
| `unstable/keyboard-shortcuts-inhibit/keyboard-shortcuts-inhibit-unstable-v1.xml` | `shortcutsinhibit/keyboard-shortcuts-inhibit-unstable-v1.xml` | `9117d9e8ec02e9a3c3c55803b41e1227e76986f555f7c12eeb29f796fa63e69b` |
| `staging/tearing-control/tearing-control-v1.xml` | `tearingcontrol/tearing-control-v1.xml` | `846895b4a90ba1ddd75673cefc596480beef318f1694ec7dbca0fec54bb27043` |
| `staging/ext-workspace/ext-workspace-v1.xml` | `workspace/ext-workspace-v1.xml` | `9b449d9d5d40f6032eba9813d18093b84f249a2cf38d6755dec7bca7eb96b0f3` |
| `staging/xdg-activation/xdg-activation-v1.xml` | `xdgactivation/xdg-activation-v1.xml` | `d8418be2d5738d50aff788bef1c7574f33f26659aa045447ff2ef9b78c58fe01` |
| `unstable/xdg-decoration/xdg-decoration-unstable-v1.xml` | `xdgdecoration/xdg-decoration-unstable-v1.xml` | `68753c4a85a28659e3aa1fe0f138329a8bf3dff0b4789f4663a2c7fedbfecf2f` |
| `unstable/xdg-output/xdg-output-unstable-v1.xml` | `xdgoutput/xdg-output-unstable-v1.xml` | `363d547c3eefe8959160cac903ff90b311d4b183005557d73640d9df2cfd7f79` |

## wlr-protocols

Source: `https://gitlab.freedesktop.org/wlroots/wlr-protocols`, commit
`bf4fc79abc359eea5a0edec0ac6d4a2b2955f82a`. The XML files retain their upstream
license grants, including the no-fee/no-advertising terms where applicable.

| Upstream path | Vendored path | SHA-256 |
|---|---|---|
| `unstable/wlr-layer-shell-unstable-v1.xml` | `layershell/wlr-layer-shell-unstable-v1.xml` | `87e0b9c837aecd6977f76f3c47d73088b7159871f5d979dc1840f6cadb5e2ed8` |
| `unstable/wlr-screencopy-unstable-v1.xml` | `screencopy/wlr-screencopy-unstable-v1.xml` | `131b8f9b4aad0c8a9cf705e90d2a1511a5ca0c477637fd3400cf1cc4fa963fb8` |
| `unstable/wlr-foreign-toplevel-management-unstable-v1.xml` | `foreigntoplevel/wlr-foreign-toplevel-management-unstable-v1.xml` | `4ecc4588858e29fe680a33521e1f22bcf22071d66d1553fe96b4ddec03d591d2` |
| `unstable/wlr-output-management-unstable-v1.xml` | `outputmanagement/wlr-output-management-unstable-v1.xml` | `65b0f82a6cf129bf1a1c31a2428795abd33886c15ddd5f3ad97e5922d7bdc3a7` |
| `unstable/wlr-output-power-management-unstable-v1.xml` | `outputpower/wlr-output-power-management-unstable-v1.xml` | `7ebd98f3449d246a57829e4b4dd9fbc3ef98e3dd42fa94ea102f14f490eb20de` |

## wlroots input protocols

Source: `https://gitlab.freedesktop.org/wlroots/wlroots`, tag `0.19.0`, peeled
commit `13a62a23a258d96f902c740310d5c7c59784a4d1`. Both XML files retain their
MIT-style license grants.

| Upstream path | Vendored path | SHA-256 |
|---|---|---|
| `protocol/virtual-keyboard-unstable-v1.xml` | `virtualkeyboard/virtual-keyboard-unstable-v1.xml` | `7ad7870003ecd592cae47dc19d277a609b7f18fd7b7be012623cf3225a7294f5` |
| `protocol/input-method-unstable-v2.xml` | `inputmethod/input-method-unstable-v2.xml` | `99414dbad9458e71aa1fa01bc45f94ca6685787bfcb4d98948f72c1b45b60703` |

## KDE server decoration

Source: `https://invent.kde.org/libraries/plasma-wayland-protocols`, tag
`v1.23.0`, peeled commit `c5ac4db818f4a575a6ccfe0065b73ecfaba6e93e`.
The XML and its generated `kdeserverdecoration` bindings retain
**LGPL-2.1-or-later**, including the copied protocol documentation. They are not
relicensed under WLTurbo's MIT license. The [license text](../LICENSES/LGPL-2.1-or-later.txt)
is included; generated files retain the upstream notice.

| Upstream path | Vendored path | SHA-256 |
|---|---|---|
| `src/protocols/server-decoration.xml` | `kdeserverdecoration/server-decoration.xml` | `c5f857734bb190dc9adfb65fb05550a6245f12af1a2be5eefd05be199805a68c` |

## Cross-package types

Cross-package references use canonical generated types. Cursor-shape depends on
`tablet`; xdg-decoration and layer-shell depend on `xdgshell`; image-copy-capture
depends on `imagecapturesource`, which depends on `extforeigntoplevel`.
