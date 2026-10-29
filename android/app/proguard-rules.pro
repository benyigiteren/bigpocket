# ===========================================================================
# BigPocket - R8 / ProGuard rules for the release build
# (release buildType: isMinifyEnabled = true, isShrinkResources = true)
#
# Sections: metadata, app model/navigation, kotlinx.serialization, OkHttp/Okio,
# ML Kit, CameraX, Guava, Coil, AndroidX/Compose and framework entry points.
# ===========================================================================

# --- Metadata / attributes -------------------------------------------------
# kotlinx.serialization, Compose and ML Kit all look information up through
# annotations and (Kotlin) runtime metadata, so keep them intact.
-keepattributes *Annotation*
-keepattributes InnerClasses, EnclosingMethod
-keepattributes Signature
-keepattributes Exceptions
-keepattributes RuntimeVisibleAnnotations, RuntimeVisibleParameterAnnotations
-keepattributes RuntimeInvisibleAnnotations, RuntimeInvisibleParameterAnnotations
-keepattributes SourceFile, LineNumberTable
-renamesourcefileattribute SourceFile

-keep class kotlin.Metadata { *; }

# --- Application classes ---------------------------------------------------
# Everything under com.ygt.bigpocket is retained: the WebSocket/DTO models are
# decoded reflectively, Navigation3 NavKeys need their generated serializers and
# the Activity/ViewModel/service entry points are created by the framework.
-keep class com.ygt.bigpocket.** { *; }
-keepclassmembers class com.ygt.bigpocket.** { *; }
-keepnames class com.ygt.bigpocket.** { *; }

# --- kotlinx.serialization -------------------------------------------------
-dontnote kotlinx.serialization.**
-dontwarn kotlinx.serialization.**
-keepclassmembers class kotlinx.serialization.json.** {
    *** Companion;
}
-keepclasseswithmembers class kotlinx.serialization.json.** {
    kotlinx.serialization.KSerializer serializer(...);
}
# Generated serializers of the app's @Serializable classes (and the companion
# that exposes them).
-keep,includedescriptorclasses class com.ygt.bigpocket.**$$serializer { *; }
-keepclassmembers class com.ygt.bigpocket.** {
    *** Companion;
}
-keepclasseswithmembers class com.ygt.bigpocket.** {
    kotlinx.serialization.KSerializer serializer(...);
}
-keep,includedescriptorclasses class **$$serializer { *; }
-keepclassmembers class **$$serializer {
    *** serializer(...);
}

# --- OkHttp / Okio ---------------------------------------------------------
# Core networking stack of the app: keep it wholesale so the WebSocket client
# and the REST calls can never be broken by shrinking/renaming.
-dontwarn okhttp3.**
-dontwarn okio.**
-dontwarn javax.annotation.**
-dontwarn org.codehaus.mojo.animal_sniffer.**
-keep class okhttp3.** { *; }
-keep interface okhttp3.** { *; }
-keepnames class okhttp3.internal.publicsuffix.PublicSuffixDatabase
-keepclassmembers class okhttp3.internal.platform.** {
    *** newInstance(...);
}
# Optional TLS providers probed at runtime through reflection.
-dontwarn org.conscrypt.**
-dontwarn org.bouncycastle.**
-dontwarn org.openjsse.**
-keep class org.conscrypt.** { *; }
-keep class org.bouncycastle.** { *; }
-keep class org.openjsse.** { *; }

# --- ML Kit barcode scanning (QR codes) ------------------------------------
# ML Kit registers its scanners reflectively and loads models dynamically.
-dontwarn com.google.mlkit.**
-keep class com.google.mlkit.** { *; }
-keep class com.google.android.gms.internal.mlkit_vision_barcode.** { *; }
-dontwarn com.google.android.gms.internal.mlkit_vision_barcode.**
-keep class com.google.android.libraries.barhopper.** { *; }
-dontwarn com.google.android.libraries.barhopper.**
-keep class com.google.android.odml.** { *; }
-dontwarn com.google.android.odml.**

# --- CameraX (webcam / screen streaming) -----------------------------------
# CameraX instantiates its config provider and the camera2/view implementation
# classes reflectively.
-dontwarn androidx.camera.**
-keep class * implements androidx.camera.core.CameraXConfig$Provider { *; }
-keepnames class androidx.camera.core.** { *; }
-keepnames class androidx.camera.camera2.** { *; }
-keepnames class androidx.camera.lifecycle.** { *; }
-keepnames class androidx.camera.view.** { *; }
-keep class androidx.camera.camera2.internal.Camera2CameraFactory { *; }
-keep class androidx.camera.lifecycle.ProcessCameraProvider { *; }
-keep class androidx.camera.view.PreviewView { *; }

# --- Guava / ListenableFuture (transitive of CameraX & ML Kit) -------------
-dontwarn com.google.common.**
-dontwarn com.google.errorprone.annotations.**
-dontwarn com.google.j2objc.annotations.**
-dontwarn com.google.auto.value.**
-dontwarn com.google.code.findbugs.**
-dontwarn org.checkerframework.**
-keep class com.google.common.util.concurrent.** { *; }
-keep class com.google.common.base.** { *; }

# --- Coil (async image loading) --------------------------------------------
-dontwarn coil.**
-dontwarn okhttp3.internal.**
-keep class coil.** { *; }
-keep interface coil.** { *; }

# --- AndroidX / Compose ----------------------------------------------------
-dontwarn androidx.compose.**
-dontwarn androidx.lifecycle.**
-keep class androidx.lifecycle.ViewModel { *; }
-keep class * extends androidx.lifecycle.ViewModel { *; }
-keep class * extends androidx.activity.ComponentActivity { *; }
-keep class androidx.navigation3.** { *; }
-keep interface androidx.navigation3.** { *; }
-keep class * implements androidx.navigation3.runtime.NavKey { *; }

# --- Framework entry points, reflection & native hooks ---------------------
-keep class * extends android.webkit.WebViewClient { *; }
-keep class * extends android.webkit.WebChromeClient { *; }
-keepclassmembers class * {
    @android.webkit.JavascriptInterface <methods>;
}
-keepclasseswithmembers class * {
    native <methods>;
}
-keepclassmembers class * implements android.os.Parcelable {
    public static final android.os.Parcelable$Creator *;
}
-keepclassmembers enum * {
    public static **[] values();
    public static ** valueOf(java.lang.String);
}
-keep class * extends android.content.BroadcastReceiver { *; }
-keep class * extends android.app.Service { *; }

# --- Kotlin / coroutines noise --------------------------------------------
-dontwarn kotlin.**
-dontwarn kotlinx.**
-dontwarn org.jetbrains.annotations.**
-dontwarn org.jetbrains.**
