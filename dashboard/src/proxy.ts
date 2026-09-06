import { NextResponse } from "next/server"
import type { NextRequest } from "next/server"

const publicRoutes = ["/login", "/signup"]

export async function proxy(request: NextRequest) {
    const { pathname } = request.nextUrl

    const isPublicRoute = publicRoutes.some((route) => pathname.startsWith(route))

    const session = request.cookies.get("session")?.value
    const csrf = request.cookies.get("csrf")?.value
    const validUser = Boolean(session && csrf)

    if (!validUser && !isPublicRoute) {
        return NextResponse.redirect(new URL("/login", request.url))
    }

    if (validUser && isPublicRoute) {
        return NextResponse.redirect(new URL("/", request.url))
    }

    // Cookie presence only; validated in page/API route.
    return NextResponse.next()
}

export const config = {
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
