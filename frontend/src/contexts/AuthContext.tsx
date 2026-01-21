import { createContext, useContext, useState, useEffect, useRef } from 'react';
import type { ReactNode } from 'react';
import { authApi } from '@/api';
import type { User } from '@/api/types';
import type { LoginRequest, RegisterRequest } from '@/api/auth';

interface AuthContextType {
  user: User | null;
  isAuthenticated: boolean;
  isLoading: boolean;
  requiresSetup: boolean;
  isSetupLoading: boolean;
  login: (credentials: LoginRequest) => Promise<void>;
  register: (data: RegisterRequest) => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

interface AuthProviderProps {
  children: ReactNode;
}

export function AuthProvider({ children }: AuthProviderProps) {
  const [user, setUser] = useState<User | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [requiresSetup, setRequiresSetup] = useState(false);
  const [isSetupLoading, setIsSetupLoading] = useState(true);
  const setupCheckedRef = useRef(false);
  const authCheckedRef = useRef(false);

  // Check setup status
  useEffect(() => {
    if (setupCheckedRef.current) {
      return;
    }
    setupCheckedRef.current = true;

    const checkSetup = async () => {
      try {
        const status = await authApi.getSetupStatus();
        setRequiresSetup(status.requires_setup);
      } catch {
        setRequiresSetup(false);
      } finally {
        setIsSetupLoading(false);
      }
    };

    checkSetup();
  }, []);

  // Initialize auth state from cookie-based session
  useEffect(() => {
    if (isSetupLoading) {
      return;
    }

    if (requiresSetup) {
      setUser(null);
      setIsLoading(false);
      return;
    }

    if (authCheckedRef.current) {
      return;
    }
    authCheckedRef.current = true;

    const initAuth = async () => {
      try {
        const currentUser = await authApi.getCurrentUser();
        setUser(currentUser);
      } catch (error) {
        setUser(null);
      } finally {
        setIsLoading(false);
      }
    };

    initAuth();
  }, [isSetupLoading, requiresSetup]);

  const login = async (credentials: LoginRequest) => {
    const response = await authApi.login(credentials);
    setRequiresSetup(false);
    setUser(response.user);
  };

  const register = async (data: RegisterRequest) => {
    const response = await authApi.register(data);
    setRequiresSetup(false);
    setUser(response.user);
  };

  const logout = async () => {
    try {
      await authApi.logout();
    } catch (error) {
      // Ignore logout errors, clear local state anyway
      console.error('Logout error:', error);
    } finally {
      setUser(null);
    }
  };

  return (
    <AuthContext.Provider
      value={{
        user,
        isAuthenticated: !!user,
        isLoading,
        requiresSetup,
        isSetupLoading,
        login,
        register,
        logout,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const context = useContext(AuthContext);
  if (context === undefined) {
    throw new Error('useAuth must be used within an AuthProvider');
  }
  return context;
}
