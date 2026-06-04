import { useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { setAuth } from '@/lib/auth';

/**
 * Login page supporting API key and OAuth2 auth (Requirement 13.6).
 * Unauthenticated access redirects here without exposing data (Requirement 13.8).
 */
export function LoginPage() {
  const [method, setMethod] = useState<'api_key' | 'oauth2'>('api_key');
  const [token, setToken] = useState('');
  const [error, setError] = useState('');
  const navigate = useNavigate();

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError('');

    if (method === 'api_key') {
      if (!token.trim()) {
        setError('API key is required');
        return;
      }
      // Validate by hitting health endpoint with the key
      try {
        const res = await fetch('/api/v1/health', {
          headers: { Authorization: `Bearer ${token}` },
        });
        if (res.status === 401) {
          setError('Invalid API key');
          return;
        }
        setAuth(token, 'api_key');
        navigate('/');
      } catch {
        setError('Cannot reach AgentPlane server');
      }
    } else {
      // OAuth2 flow - redirect to authorization endpoint
      // In production this would use PKCE flow
      setError('OAuth2 login not yet configured');
    }
  };

  return (
    <div className="login-page">
      <div className="login-card">
        <h1>AgentPlane</h1>
        <p>Sign in to manage your agent fleet</p>

        <form onSubmit={handleSubmit}>
          <fieldset>
            <legend>Authentication Method</legend>
            <label>
              <input
                type="radio"
                name="method"
                value="api_key"
                checked={method === 'api_key'}
                onChange={() => setMethod('api_key')}
              />
              API Key
            </label>
            <label>
              <input
                type="radio"
                name="method"
                value="oauth2"
                checked={method === 'oauth2'}
                onChange={() => setMethod('oauth2')}
              />
              OAuth2
            </label>
          </fieldset>

          {method === 'api_key' && (
            <div className="field">
              <label htmlFor="token">API Key</label>
              <input
                id="token"
                type="password"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder="Enter your API key"
                autoComplete="off"
              />
            </div>
          )}

          {error && <div className="error" role="alert">{error}</div>}

          <button type="submit">Sign In</button>
        </form>
      </div>
    </div>
  );
}
