import { NextResponse } from "next/server"
import type { NextRequest } from "next/server"

const publicRoutes = ["/login", "/signup"]

export async function proxy(request: NextRequest) {
    const { pathname } = request.nextUrl

    // Check if the requested path is a public route
    const isPublicRoute = publicRoutes.some((route) => pathname.startsWith(route))

    const validUser = request.cookies.has("session") && request.cookies.has("csrf")

    // If user is not authenticated and trying to access a protected route
    // Redirect unauthenticated users to login
    if (!validUser && !isPublicRoute) {
        // Redirect to login page
        return NextResponse.redirect(new URL("/login", request.url))
    }

    // Since user is authenticated and trying to access a public route
    // Redirect authenticated users away from login
    if (validUser && isPublicRoute) {
        // Redirect to dashboard (root)
        return NextResponse.redirect(new URL("/", request.url))
    }

    // For protected routes, we only check cookie existence
    // Actual validation happens in the page or API route
    return NextResponse.next()
}

// Configure which paths should be processed by the middleware
export const config = {
    // Apply to all routes except for static files, api routes, etc.
    matcher: [
        /*
         * Match all request paths except for the ones starting with:
         * - _next/static (static files)
         * - _next/image (image optimization files)
         * - favicon.ico (favicon file)
         * - public folder
         * - api routes
         */
        "/((?!_next/static|_next/image|favicon.ico|public|api).*)",
    ],
}
