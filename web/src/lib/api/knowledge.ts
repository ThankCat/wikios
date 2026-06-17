import { api } from "@/lib/api";

export const knowledgeAPI = {
  tree: api.wikiTree,
  file: api.wikiFile,
  downloadURL: api.wikiDownloadURL,
  syncStatus: api.syncStatus,
  syncCommit: api.syncCommit,
  syncGenerateMessage: api.syncGenerateMessage,
  syncPush: api.syncPush,
  syncPull: api.syncPull,
};
