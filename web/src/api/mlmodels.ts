import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call, type Schemas } from './client'

const modelKeys = {
  all: ['ml-models'] as const,
  keys: (name: string) => ['ml-models', name, 'keys'] as const,
}

/** Your models, by name, with their workers connected now: looked at again every 10 s. */
export function useMLModels() {
  return useQuery({
    queryKey: modelKeys.all,
    queryFn: () => call(api.GET('/models')),
    refetchInterval: 10_000,
  })
}

/** Makes a model. */
export function useCreateMLModel() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (body: Schemas['CreateMLModelRequest']) => call(api.POST('/models', { body })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: modelKeys.all }),
  })
}

/** Deletes a model and its keys. */
export function useDeleteMLModel() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (name: string) =>
      call(api.DELETE('/models/{name}', { params: { path: { name } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: modelKeys.all }),
  })
}

/** A worker model's keys, the newest first. */
export function useWorkerKeys(name: string) {
  return useQuery({
    queryKey: modelKeys.keys(name),
    queryFn: () => call(api.GET('/models/{name}/keys', { params: { path: { name } } })),
  })
}

/** Makes a worker key: its secret comes back this once. */
export function useCreateWorkerKey(model: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (name: string) =>
      call(api.POST('/models/{name}/keys', { params: { path: { name: model } }, body: { name } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: modelKeys.keys(model) }),
  })
}

/** Revokes a worker key. */
export function useDeleteWorkerKey(model: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) =>
      call(api.DELETE('/models/{name}/keys/{id}', { params: { path: { name: model, id } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: modelKeys.keys(model) }),
  })
}
