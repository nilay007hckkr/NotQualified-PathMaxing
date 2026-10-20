import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, api } from '../api/client';
import type { User } from '../api/types';

export const meKey = ['auth', 'me'] as const;

/**
 * The signed-in user, or null when the session is missing or expired.
 * A 401 is an answer ("not signed in"), not an error, so it isn't retried.
 */
export function useMe() {
  return useQuery<User | null, ApiError>({
    queryKey: meKey,
    queryFn: async () => {
      try {
        const { user } = await api.auth.me();
        return user;
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) return null;
        throw err;
      }
    },
    staleTime: 5 * 60 * 1000,
    retry: (count, err) => err.status !== 401 && count < 2,
  });
}

export function useLogin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: api.auth.login,
    onSuccess: ({ user }) => qc.setQueryData(meKey, user),
  });
}

/** Signing up also signs the user in: the server sets the session cookie. */
export function useSignup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: api.auth.signup,
    onSuccess: ({ user }) => qc.setQueryData(meKey, user),
  });
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: api.auth.logout,
    onSettled: () => {
      // Drop every cached response: none of it belongs to the next user.
      qc.clear();
      qc.setQueryData(meKey, null);
    },
  });
}
