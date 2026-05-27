import { api } from "@/lib/api";

export const modelsAPI = {
  list: api.listLLMModels,
  get: api.getLLMModel,
  create: api.createLLMModel,
  update: api.updateLLMModel,
  delete: api.deleteLLMModel,
  activate: api.activateLLMModel,
  test: api.testLLMModel,
};

