/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_GRAPHI_URL?: string;
}
interface ImportMeta {
  readonly env: ImportMetaEnv;
}
