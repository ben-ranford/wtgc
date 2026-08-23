# Changelog

## 1.0.0 (2026-08-22)


### Features

* **ci:** enforce pull request metadata ([#47](https://github.com/ben-ranford/wtgc/issues/47)) ([fcc5585](https://github.com/ben-ranford/wtgc/commit/fcc55857a2f0f847c530525d1882965a6cc9bfc6))
* **ci:** migrate to GitHub-hosted runners ([#18](https://github.com/ben-ranford/wtgc/issues/18)) ([1c43a4d](https://github.com/ben-ranford/wtgc/commit/1c43a4d9ba4991001e1114e7a3988edcfb37b309))
* **core:** ship production-ready worktree garbage collector ([#2](https://github.com/ben-ranford/wtgc/issues/2)) ([1fa5d52](https://github.com/ben-ranford/wtgc/commit/1fa5d52eba2959f6eea028b9a9ff6e3ee12fa3c2))
* port queue-me pull request queue ([#23](https://github.com/ben-ranford/wtgc/issues/23)) ([7f8a81b](https://github.com/ben-ranford/wtgc/commit/7f8a81beb73abcc03d2460686103a60e5347aa28))


### Bug Fixes

* **app:** reduce cleanup complexity ([#26](https://github.com/ben-ranford/wtgc/issues/26)) ([75ef138](https://github.com/ben-ranford/wtgc/commit/75ef138b437fb56e72a93bf4200044872f69d059))
* **app:** reduce repository scan complexity ([#21](https://github.com/ben-ranford/wtgc/issues/21)) ([01a223c](https://github.com/ben-ranford/wtgc/commit/01a223c567f8a2f468f0d338903aff784b6a8b5e))
* **app:** reduce worktree classification complexity ([#24](https://github.com/ben-ranford/wtgc/issues/24)) ([ae9b20d](https://github.com/ben-ranford/wtgc/commit/ae9b20d709ff5903c7d35588d6f4db1402ca670c))
* **ci:** eliminate metadata enforcement Sonar findings ([#74](https://github.com/ben-ranford/wtgc/issues/74)) ([b1d5d3d](https://github.com/ben-ranford/wtgc/commit/b1d5d3d56fa8029b42926ca4f26bfe17250d402c))
* **ci:** pass build SHA through environment ([#33](https://github.com/ben-ranford/wtgc/issues/33)) ([392073d](https://github.com/ben-ranford/wtgc/commit/392073d85f663bd6c822fe591ab465f1c1405459))
* **ci:** pass release SHA through environment ([#27](https://github.com/ben-ranford/wtgc/issues/27)) ([42b1eaf](https://github.com/ben-ranford/wtgc/commit/42b1eaf6e1d4edcbcf44127b364c56e25aeb0ba9))
* **ci:** pass release tag through environment ([#29](https://github.com/ben-ranford/wtgc/issues/29)) ([5b91da1](https://github.com/ben-ranford/wtgc/commit/5b91da135ce2d50ff8e7f6a2a7cc71360b9cb852))
* **ci:** pass SBOM tag through environment ([#42](https://github.com/ben-ranford/wtgc/issues/42)) ([7423059](https://github.com/ben-ranford/wtgc/commit/7423059186ec474798230ce6927d1c43b8b1da43))
* **ci:** remove privileged workflow checkout ([#77](https://github.com/ben-ranford/wtgc/issues/77)) ([09d1cf8](https://github.com/ben-ranford/wtgc/commit/09d1cf81d868665f9efb631bd3324200e095427c))
* **ci:** restore metadata quality gate ([#75](https://github.com/ben-ranford/wtgc/issues/75)) ([53ce4a0](https://github.com/ben-ranford/wtgc/commit/53ce4a0b010b74d0943ec70071b45a8013b298d0))
* **ci:** scope contents permission to release job ([#25](https://github.com/ben-ranford/wtgc/issues/25)) ([ba2bcf0](https://github.com/ben-ranford/wtgc/commit/ba2bcf0d0b2277809b73b3f928c0ede4e5284a54))
* **ci:** scope issues permission to release job ([#20](https://github.com/ben-ranford/wtgc/issues/20)) ([e482e85](https://github.com/ben-ranford/wtgc/commit/e482e85714bd939a9c8d916c61b5429506512394))
* **ci:** scope pull request permission to release job ([#22](https://github.com/ben-ranford/wtgc/issues/22)) ([de17eac](https://github.com/ben-ranford/wtgc/commit/de17eacfa341bc2ee2119d7be47ea570eb04fd81))
* **ci:** skip metadata checks for Renovate PRs ([#78](https://github.com/ben-ranford/wtgc/issues/78)) ([10fdbd7](https://github.com/ben-ranford/wtgc/commit/10fdbd7f9b4467502521e85a77238914c3060d01))
* **cli:** reduce option parsing complexity ([#28](https://github.com/ben-ranford/wtgc/issues/28)) ([8be6891](https://github.com/ben-ranford/wtgc/commit/8be6891170294e7f52b82f4650d83b5e8789f011))
* **gitx:** document no-op command cancel ([#46](https://github.com/ben-ranford/wtgc/issues/46)) ([b45592c](https://github.com/ben-ranford/wtgc/commit/b45592cdebb5b42808a70db1f3db63510007a1f7))
* **gitx:** reduce discovery complexity ([#38](https://github.com/ben-ranford/wtgc/issues/38)) ([4a7ebd4](https://github.com/ben-ranford/wtgc/commit/4a7ebd485312caea11e9a29c8520b17a4facb6a1))
* **gitx:** reduce worktree parser complexity ([#43](https://github.com/ben-ranford/wtgc/issues/43)) ([9322357](https://github.com/ben-ranford/wtgc/commit/9322357cc166eea64ee9ea1881e70ce8e2fba74b))
* **gitx:** reuse local branch ref prefix ([#44](https://github.com/ben-ranford/wtgc/issues/44)) ([809ecd6](https://github.com/ben-ranford/wtgc/commit/809ecd699dc00ccb530075ed3129d31b6821c2e4))
* **gitx:** reuse rev-parse command ([#45](https://github.com/ben-ranford/wtgc/issues/45)) ([4e5ad75](https://github.com/ben-ranford/wtgc/commit/4e5ad75899a9c9a83139b412fce02f1a56ac6111))
* skip conflicted queue entries ([#80](https://github.com/ben-ranford/wtgc/issues/80)) ([e68093b](https://github.com/ben-ranford/wtgc/commit/e68093bd357a20e6e8483162587b88df8dc28cf9))
* **sonar:** add release path default case ([#40](https://github.com/ben-ranford/wtgc/issues/40)) ([311ba43](https://github.com/ben-ranford/wtgc/commit/311ba43444ecf9f4ce0bab425ee7789048a9a681))
* **sonar:** add source path default case ([#41](https://github.com/ben-ranford/wtgc/issues/41)) ([83f3dd3](https://github.com/ben-ranford/wtgc/commit/83f3dd3c8a4f74b45d659d04bc7b2f699904343c))
* **sonar:** extract classification type ([#31](https://github.com/ben-ranford/wtgc/issues/31)) ([0a8fabd](https://github.com/ben-ranford/wtgc/commit/0a8fabdffb724e7b87b23f70363b40fa99aa5e3d))
* **sonar:** extract schema version type ([#30](https://github.com/ben-ranford/wtgc/issues/30)) ([3044de0](https://github.com/ben-ranford/wtgc/commit/3044de07b397d73ea89de81ed91222cad0acc555))
* **sonar:** resolve branch check git path ([#35](https://github.com/ben-ranford/wtgc/issues/35)) ([ae5f7ba](https://github.com/ben-ranford/wtgc/commit/ae5f7ba34ccb853011fe4f442f56db8fd106cec8))
* **sonar:** resolve git executable path ([#32](https://github.com/ben-ranford/wtgc/issues/32)) ([63f7ad0](https://github.com/ben-ranford/wtgc/commit/63f7ad0209f25cd2979f0156c3c679ca336e1e58))
* **sonar:** reuse commit message prefix ([#34](https://github.com/ben-ranford/wtgc/issues/34)) ([2b2947f](https://github.com/ben-ranford/wtgc/commit/2b2947fe5b24bdfce0c4caa6b88486fbc0498512))
* **sonar:** reuse keep fixture content ([#39](https://github.com/ben-ranford/wtgc/issues/39)) ([a5c15b3](https://github.com/ben-ranford/wtgc/commit/a5c15b39201b65182ffd339afa9aee6850bfb6a4))
