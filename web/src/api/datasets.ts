import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call } from './client'

const datasetKeys = {
  all: ['datasets'] as const,
  rows: (name: string) => ['datasets', name, 'rows'] as const,
  usage: ['datasets', 'usage'] as const,
}

/** Your datasets, by name. */
export function useDatasets() {
  return useQuery({
    queryKey: datasetKeys.all,
    queryFn: () => call(api.GET('/datasets')),
  })
}

/** A dataset's first rows, to look at. */
export function useDatasetRows(name: string, limit = 20, enabled = true) {
  return useQuery({
    queryKey: [...datasetKeys.rows(name), limit],
    queryFn: () =>
      call(api.GET('/datasets/{name}/rows', { params: { path: { name }, query: { limit } } })),
    enabled,
  })
}

/** Deletes a dataset and its rows. */
export function useDeleteDataset() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (name: string) =>
      call(api.DELETE('/datasets/{name}', { params: { path: { name } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: datasetKeys.all }),
  })
}

/** What each user's datasets take, against what each may: an admin's. */
export function useDatasetUsage() {
  return useQuery({
    queryKey: datasetKeys.usage,
    queryFn: () => call(api.GET('/admin/datasets/usage')),
    retry: false,
  })
}

/** Where a dataset downloads as CSV. */
export const datasetCSV = (name: string) => `/api/v1/datasets/${encodeURIComponent(name)}/csv`
