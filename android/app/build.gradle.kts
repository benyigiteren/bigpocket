plugins {
  alias(libs.plugins.android.application)
  alias(libs.plugins.compose.compiler)
  alias(libs.plugins.kotlin.serialization)
}

// ---------------------------------------------------------------------------
// Release signing
// ---------------------------------------------------------------------------
// Secrets are read from the environment first (CI passes them through
// BP_KEYSTORE_PASSWORD / BP_KEY_ALIAS / BP_KEY_PASSWORD), with the historical
// values kept as local fallbacks.
//
// A signed release APK is only produced when `android/app/release.jks` exists
// (locally or decoded from the ANDROID_KEYSTORE_BASE64 secret in CI).
// When it is missing we fall back to the standard Android debug keystore so
// `assembleDebug` and `assembleRelease` both keep working out of the box.
val releaseKeystoreFile = file("release.jks")
val hasReleaseKeystore = releaseKeystoreFile.exists()
val releaseStorePassword = System.getenv("BP_KEYSTORE_PASSWORD") ?: "bigpocket123"
val releaseKeyAlias = System.getenv("BP_KEY_ALIAS") ?: "bigpocket"
val releaseKeyPassword = System.getenv("BP_KEY_PASSWORD") ?: "bigpocket123"

android {
    namespace = "com.ygt.bigpocket"
    compileSdk = 36
    defaultConfig {
        applicationId = "com.ygt.bigpocket"
        minSdk = 24
        targetSdk = 35
        versionCode = 7
        versionName = "1.4.2-beta"

        // Ship only real device ABIs; x86/x86_64 emulator payloads only bloat
        // the release APK (the emulator is a debug-only target).
        ndk {
            abiFilters += listOf("arm64-v8a", "armeabi-v7a")
        }
    }

    // See the notes above the file: only created when release.jks is present.
    signingConfigs {
        if (hasReleaseKeystore) {
            create("release") {
                storeFile = releaseKeystoreFile
                storePassword = releaseStorePassword
                keyAlias = releaseKeyAlias
                keyPassword = releaseKeyPassword
                enableV1Signing = true
                enableV2Signing = true
                enableV3Signing = true
                enableV4Signing = true
            }
        }
    }

    buildTypes {
        release {
            // Shrinking the code/resources is what keeps the APK well below
            // the 40MB+ it used to be.
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            signingConfig = if (hasReleaseKeystore) {
                signingConfigs.getByName("release")
            } else {
                signingConfigs.getByName("debug")
            }
        }
        debug {
            // defaultConfig limits the packaged ABIs to real devices (that is
            // what keeps the release APK small); re-add the emulator ABI so
            // local debug builds still install on an x86_64 emulator.
            ndk {
                abiFilters.clear()
                abiFilters += listOf("arm64-v8a", "armeabi-v7a", "x86_64")
            }
            signingConfig = if (hasReleaseKeystore) {
                signingConfigs.getByName("release")
            } else {
                signingConfigs.getByName("debug")
            }
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    buildFeatures {
      compose = true
      aidl = false
      buildConfig = false
      shaders = false
    }

    packaging {
      resources {
        excludes += "/META-INF/{AL2.0,LGPL2.1}"
      }
    }
}

kotlin {
    jvmToolchain(17)
}

dependencies {
  val composeBom = platform(libs.androidx.compose.bom)
  implementation(composeBom)
  androidTestImplementation(composeBom)

  // Core Android dependencies
  implementation(libs.androidx.core.ktx)
  implementation(libs.androidx.lifecycle.runtime.ktx)
  implementation(libs.androidx.activity.compose)

  // Arch Components
  implementation(libs.androidx.lifecycle.runtime.compose)
  implementation(libs.androidx.lifecycle.viewmodel.compose)

  // Compose
  implementation(libs.androidx.compose.ui)
  implementation(libs.androidx.compose.ui.tooling.preview)
  implementation(libs.androidx.compose.material3)
  // Tooling
  debugImplementation(libs.androidx.compose.ui.tooling)
  // Instrumented tests
  androidTestImplementation(libs.androidx.compose.ui.test.junit4)
  debugImplementation(libs.androidx.compose.ui.test.manifest)

  // Local tests: jUnit, coroutines, Android runner
  testImplementation(libs.junit)
  testImplementation(libs.kotlinx.coroutines.test)

  // Instrumented tests: jUnit rules and runners
  androidTestImplementation(libs.androidx.test.core)
  androidTestImplementation(libs.androidx.test.ext.junit)
  androidTestImplementation(libs.androidx.test.runner)
  androidTestImplementation(libs.androidx.test.espresso.core)

  // Navigation
  implementation(libs.androidx.navigation3.ui)
  implementation(libs.androidx.navigation3.runtime)
  implementation(libs.androidx.lifecycle.viewmodel.navigation3)

  // OkHttp for WebSocket Connection
  implementation("com.squareup.okhttp3:okhttp:4.12.0")

  // ML Kit Barcode Scanning (for QR Codes)
  implementation("com.google.mlkit:barcode-scanning:17.2.0")

  // Material Icons
  implementation("androidx.compose.material:material-icons-core")
  implementation("androidx.compose.material:material-icons-extended")

  // CameraX for Webcam Streaming
  val cameraxVersion = "1.3.1"
  implementation("androidx.camera:camera-core:$cameraxVersion")
  implementation("androidx.camera:camera-camera2:$cameraxVersion")
  implementation("androidx.camera:camera-lifecycle:$cameraxVersion")
  implementation("androidx.camera:camera-view:$cameraxVersion")

  // Coil for async network image loading
  implementation("io.coil-kt:coil-compose:2.6.0")
}

