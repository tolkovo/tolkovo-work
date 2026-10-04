import { useLocation, useRouter } from "@tanstack/react-router";
import { useEffect, useState } from "react";

/**
 * The token of an emailed link (invitation, password reset, email confirmation). The server
 * puts it in the URL fragment, which browsers send neither to servers nor in Referer. The page
 * reads it once and then drops it from the address bar and the history entry, so it is not
 * left behind in the browser history or shown over someone's shoulder.
 */
export function useLinkToken(): string {
  const location = useLocation();
  const router = useRouter();
  const [token] = useState(() => location.hash);

  useEffect(() => {
    if (location.hash) router.history.replace(location.pathname + location.searchStr);
  }, [location.hash, location.pathname, location.searchStr, router.history]);

  return token;
}
