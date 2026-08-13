import { QueryClient } from '@tanstack/react-query'

// 401 handling lives in the API client (see setUnauthorizedListener there), not
// in a QueryCache onError handler — that way direct fetches get the same
// redirect-to-login behaviour as queries do.
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: false,
      refetchOnWindowFocus: false,
    },
    mutations: {
      retry: false,
    },
  },
})
